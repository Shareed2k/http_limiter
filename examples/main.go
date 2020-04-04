package main

import (
	"log"
	"net/http"
	"time"

	"github.com/go-redis/redis/v7"
	limiter "github.com/shareed2k/http_limiter"
)

func main() {
	option, err := redis.ParseURL("redis://127.0.0.1:6379/0")
	if err != nil {
		log.Fatal(err)
	}
	client := redis.NewClient(option)
	_ = client.FlushDB().Err()

	// 3 requests per 10 seconds max
	cfg := limiter.Config{
		Rediser:   client,
		Max:       3,
		Burst:     3,
		Period:    10 * time.Second,
		Algorithm: limiter.GCRAAlgorithm,
	}

	limiterHandler := limiter.NewWithConfig(cfg)

	var myHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello world"))
	})

	log.Fatal(http.ListenAndServe(":3000", limiterHandler(myHandler)))
}
