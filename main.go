package http_limiter

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v7"
	"github.com/shareed2k/go_limiter"
)

const (
	SlidingWindowAlgorithm = go_limiter.SlidingWindowAlgorithm
	GCRAAlgorithm          = go_limiter.GCRAAlgorithm
	DefaultKeyPrefix       = "http_limiter"
)

var (
	DefaultConfig = Config{
		Skipper:    DefaultSkipper,
		Max:        10,
		Burst:      10,
		Prefix:     DefaultKeyPrefix,
		Algorithm:  SlidingWindowAlgorithm,
		StatusCode: http.StatusTooManyRequests,
		Message:    "Too many requests, please try again later.",
		Period:     time.Minute,
		Key: func(r *http.Request) string {
			return GetIP(r).String()
		},
	}
)

type (
	Config struct {
		Skipper Skipper

		// Rediser
		Rediser *redis.Client

		// Max number of recent connections
		// Default: 10
		Max int

		// Burst
		// Default: 10
		Burst int

		// StatusCode
		// Default: 429 Too Many Requests
		StatusCode int

		// Message
		// default: "Too many requests, please try again later."
		Message string

		// Algorithm
		// Default: sliding window
		Algorithm uint

		// Prefix
		// Default: http_limiter
		Prefix string

		// SkipOnError
		// Default: false
		SkipOnError bool

		// Period
		// Default: 1m
		Period time.Duration

		// Key allows to use a custom handler to create custom keys
		// Default: func(r *http.Request) string {
		//   return GetIP(r).String()
		// }
		Key func(r *http.Request) string

		// Handler is called when a request hits the limit
		// Default: func(w http.ResponseWriter, r *http.Request) {
		//   w.Write([]byte(config.Message))
		//	 w.WriteHeader(config.StatusCode)
		// }
		Handler func(http.ResponseWriter, *http.Request)

		// ErrHandler is called when a error happen inside go_limiiter lib
		// Default: func(w http.ResponseWriter, r *http.Request) {
		// 	 w.Write([]byte(err.Error()))
		//	 w.WriteHeader(http.StatusInternalServerError)
		// }
		ErrHandler func(error, http.ResponseWriter, *http.Request)
	}
	Skipper func(*http.Request) bool
)

func New(rediser *redis.Client) func(http.Handler) http.Handler {
	config := DefaultConfig
	config.Rediser = rediser
	return NewWithConfig(config)
}

func NewWithConfig(config Config) func(http.Handler) http.Handler {
	if config.Rediser == nil {
		panic(errors.New("redis client is missing"))
	}

	if config.Skipper == nil {
		config.Skipper = DefaultConfig.Skipper
	}

	if config.Max == 0 {
		config.Max = DefaultConfig.Max
	}

	if config.Burst == 0 {
		config.Burst = DefaultConfig.Burst
	}

	if config.StatusCode == 0 {
		config.StatusCode = DefaultConfig.StatusCode
	}

	if config.Message == "" {
		config.Message = DefaultConfig.Message
	}

	if config.Algorithm == 0 {
		config.Algorithm = DefaultConfig.Algorithm
	}

	if config.Prefix == "" {
		config.Prefix = DefaultConfig.Prefix
	}

	if config.Period == 0 {
		config.Period = DefaultConfig.Period
	}

	if config.Key == nil {
		config.Key = DefaultConfig.Key
	}

	if config.Handler == nil {
		config.Handler = func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(config.StatusCode)
			w.Write([]byte(config.Message))
		}
	}

	if config.ErrHandler == nil {
		config.ErrHandler = func(err error, w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
		}
	}

	limiter := go_limiter.NewLimiter(config.Rediser)
	limit := &go_limiter.Limit{
		Period:    config.Period,
		Algorithm: config.Algorithm,
		Rate:      int64(config.Max),
		Burst:     int64(config.Burst),
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config.Skipper(r) {
				next.ServeHTTP(w, r)

				return
			}

			result, err := limiter.Allow(config.Key(r), limit)
			if err != nil {
				if config.SkipOnError {
					next.ServeHTTP(w, r)

					return
				}

				config.ErrHandler(err, w, r)

				return
			}

			// Check if hits exceed the max
			if !result.Allowed {
				// Return response with Retry-After header
				// https://tools.ietf.org/html/rfc6584
				w.Header().Set("Retry-After", strconv.FormatInt(time.Now().Add(result.RetryAfter).Unix(), 10))

				// Call Handler func
				config.Handler(w, r)

				return
			}

			// We can continue, update RateLimit headers
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(config.Max))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(result.ResetAfter).Unix(), 10))

			next.ServeHTTP(w, r)
		})
	}
}

// DefaultSkipper returns false which processes the middleware.
func DefaultSkipper(r *http.Request) bool {
	return false
}

// GetIP returns IP address from request.
// It will lookup IP in X-Forwarded-For and X-Real-IP headers.
func GetIP(r *http.Request) net.IP {
	ip := r.Header.Get("X-Forwarded-For")
	if ip != "" {
		parts := strings.SplitN(ip, ",", 2)
		part := strings.TrimSpace(parts[0])
		return net.ParseIP(part)
	}

	ip = strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if ip != "" {
		return net.ParseIP(ip)
	}

	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return net.ParseIP(remoteAddr)
	}

	return net.ParseIP(host)
}
