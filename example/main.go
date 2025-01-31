package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/n-r-w/smoother"
	"github.com/n-r-w/smoother/breaker"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func main() {
	const (
		rps         = 10000
		clientCount = 10
		// redis_rate or throttled limiter
		useRedisRate = true
		// use miniredis or redis cluster
		fakeRedis = false
		// request to tryer timeout
		requestTimeout = time.Millisecond * 500

		// number of errors before the circuit breaker opens
		breakerErrorThreshold = 1
		// number of successes before the circuit breaker closes
		breakerSuccessThreshold = 3
		// interval between health checks.
		// transition to closed state after breakerSuccessThreshold*breakerHealthCheckInterval
		breakerHealthCheckInterval = time.Second * 5
		// maximum duration of a health check
		breakerHealthCheckMaxDuration = time.Millisecond * 500
		// maximum duration of a main run function
		mainRunFallbackTimeout = time.Millisecond * 500
		// maximum duration of a fallback run function
		fallbackRunFallbackTimeout = time.Millisecond * 500
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
	var fallbackTryer *smoother.FallbackTryer
	if fallbackTryer, err = smoother.NewFallbackTryer(tryer, localTryer,
		smoother.WithFallbackTryerBreakerOptions(
			breaker.WithErrorThreshold(breakerErrorThreshold),
			breaker.WithSuccessThreshold(breakerSuccessThreshold),
			breaker.WithHealthCheckInterval(breakerHealthCheckInterval),
			breaker.WithHealthCheckMaxDuration(breakerHealthCheckMaxDuration),
			breaker.WithJitterMaxPerc(0.1), //nolint:mnd // jitter 10%
			breaker.WithStateChangeFunc(
				func(_ context.Context, state breaker.State) {
					fmt.Printf("Circuit breaker state changed: %s\n", state)
				},
			),
		),
		smoother.WithFallbackTryerBreakerRunOptions(
			breaker.WithRunFallbackTimeout(mainRunFallbackTimeout),
			breaker.WithRunFallbackTimeout(fallbackRunFallbackTimeout),
		),
	); err != nil {
		log.Fatal(err)
	}

	if err = fallbackTryer.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := fallbackTryer.Stop(); err != nil {
			log.Fatal(err)
		}
	}()

	sm, err := smoother.NewRateSmoother(fallbackTryer, smoother.WithTimeout(requestTimeout))
	if err != nil {
		log.Fatal(err) //nolint:gocritic // ok
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
