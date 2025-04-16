package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/n-r-w/smoother"
	"github.com/n-r-w/smoother/breaker"
	"github.com/n-r-w/smoother/throttler"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	rps         = 500.0
	clientCount = 6
	// use miniredis or redis cluster
	fakeRedis = true
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
	// maximum duration of a primary run function
	runPrimaryTimeout = time.Millisecond * 100
	// maximum duration of a fallback run function
	runFallbackTimeout = time.Millisecond * 100

	// error rate
	errorRate = 0

	// throttler max concurrency
	throttlerMaxConcurrency = 1
)

func main() {
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
		client = redis.NewClient(&redis.Options{
			Addr: "localhost:6379",
		})
	}

	// in memory tryer
	localTryer, err := smoother.NewLocalTryer(rps)
	if err != nil {
		log.Fatal(err)
	}

	// redis tryer
	redisRateTryer, err := smoother.NewRedisRateTryer(client, "prefix", "test", rps)
	if err != nil {
		log.Fatal(err)
	}

	// Add circuit breaker with inmemory fallback
	var fallbackTryer *smoother.FallbackTryer
	if fallbackTryer, err = smoother.NewFallbackTryer(redisRateTryer, localTryer,
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
			breaker.WithRunPrimaryTimeout(runPrimaryTimeout),
			breaker.WithRunFallbackTimeout(runFallbackTimeout),
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

	sm.Start()
	defer sm.Stop()

	th, err := throttler.New(throttlerMaxConcurrency, throttler.WithTimeout(requestTimeout))
	if err != nil {
		log.Fatal(err)
	}

	// generate rps
	generateRPS(sm, th, clientCount)

	time.Sleep(time.Hour)
}

func generateRPS(smoother *smoother.RateSmoother, th *throttler.Throttler, clientCount int) {
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

	for range clientCount {
		go func() {
			for {
				ctxTry := ctx
				// imitation of context cancellation for some requests
				if rand.Float64() < errorRate { //nolint:gosec,mnd //ok
					ctxTry, _ = context.WithTimeout(ctx, time.Millisecond)
				}

				err := th.Execute(ctxTry, func(ctx context.Context) error {
					_, err := smoother.Take(ctx, 1)
					return err
				})
				if err != nil {
					errorCount.Add(1)
				} else {
					successCount.Add(1)
				}
			}
		}()
	}
}
