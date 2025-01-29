package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/n-r-w/smoother"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func main() {
	const (
		rps            = 500
		clientCount    = 10
		useRedisRate   = true                   // redis_rate or throttled limiter
		fakeRedis      = true                   // use miniredis or redis cluster
		requestTimeout = time.Millisecond * 500 // request to tryer timeout
		// circuit breaker
		breakerErrorThreshold   = 1
		breakerSuccessThreshold = 3
		breakerTimeout          = time.Second * 3
	)

	var client redis.UniversalClient
	if fakeRedis {
		mr, err := miniredis.Run()
		if err != nil {
			log.Fatal(err)
		}

		client = redis.NewClient(&redis.Options{
			Addr: mr.Addr(),
		})
	} else {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs: []string{
				"localhost:6379",
				"localhost:6380",
				"localhost:6381",
				"localhost:6382",
				"localhost:6383",
				"localhost:6384",
			},
		})
	}

	// in memory tryer
	localTryer, err := smoother.NewLocalTryer(rps)
	if err != nil {
		log.Fatal(err)
	}

	// github.com/throttled/throttled/v2 tryer
	throttledTryer, err := smoother.NewRedisThrottledTryer(client, "test", rps)
	if err != nil {
		log.Fatal(err)
	}

	// github.com/go-redis/redis_rate/v10 tryer
	redisRateTryer, err := smoother.NewRedisRateTryer(client, "test", rps)
	if err != nil {
		log.Fatal(err)
	}

	var tryer smoother.Tryer

	if useRedisRate {
		tryer = redisRateTryer
	} else {
		tryer = throttledTryer
	}

	// Add circuit breaker with inmemory fallback
	tryer, err = smoother.NewFallbackTryer(tryer, localTryer,
		smoother.WithFallbackTryerErrorThreshold(breakerErrorThreshold),
		smoother.WithFallbackTryerSuccessThreshold(breakerSuccessThreshold),
		smoother.WithFallbackTryerTimeout(breakerTimeout),
		smoother.WithFallbackTryerStatusChangedFunc(
			func(_ context.Context, status smoother.FallbackTryerStatus) {
				fmt.Printf("FallbackTryer status changed: %s\n", status)
			}),
	)
	if err != nil {
		log.Fatal(err)
	}

	sm, err := smoother.NewRateSmoother(tryer, smoother.WithTimeout(requestTimeout))
	if err != nil {
		log.Fatal(err)
	}

	// generate rps
	generateRPS(sm, clientCount)

	time.Sleep(time.Hour)
}

func generateRPS(smoother *smoother.RateSmoother, clientCount int) {
	var (
		successCount atomic.Int64
		errorCount   atomic.Int64
		start        = time.Now()
		ctx          = context.Background()
	)

	go func() {
		for {
			time.Sleep(time.Second)
			fmt.Printf("Success: %d, Error: %d, Success RPS: %f, Error RPS: %f\n",
				successCount.Load(),
				errorCount.Load(),
				float64(successCount.Load())/time.Since(start).Seconds(),
				float64(errorCount.Load())/time.Since(start).Seconds(),
			)
		}
	}()

	for i := 0; i < clientCount; i++ {
		go func() {
			for {
				_, err := smoother.Take(ctx, 1)
				if err != nil {
					errorCount.Add(1)
				} else {
					successCount.Add(1)
				}
			}
		}()
	}
}
