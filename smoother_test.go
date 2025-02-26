//nolint:revive // ok
package smoother

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type getTestTryer func(rps int) Tryer

func setupThrottledTryer(t *testing.T) getTestTryer {
	t.Helper()

	mr := miniredis.RunT(t)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())

	tryerGetter := func(rps int) Tryer {
		tryer, err := NewRedisThrottledTryer(
			client, "test", rps)
		require.NoError(t, err)
		return tryer
	}

	return tryerGetter
}

func setupRedisRateTryer(t *testing.T) getTestTryer {
	t.Helper()

	mr := miniredis.RunT(t)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())

	tryerGetter := func(rps int) Tryer {
		tryer, err := NewRedisRateTryer(client, "test", rps)
		require.NoError(t, err)
		return tryer
	}

	return tryerGetter
}

func setupTestLocalTryer(t *testing.T) getTestTryer {
	t.Helper()

	tryerGetter := func(rps int) Tryer {
		tryer, err := NewLocalTryer(rps)
		require.NoError(t, err)
		return tryer
	}

	return tryerGetter
}

func TestRateSmoother_Take(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		testRateSmoother_Take_helper(t, setupTestLocalTryer(t))
	})
	t.Run("redis_rate", func(t *testing.T) {
		testRateSmoother_Take_helper(t, setupThrottledTryer(t))
	})
	t.Run("redis_rate", func(t *testing.T) {
		testRateSmoother_Take_helper(t, setupRedisRateTryer(t))
	})
}

func testRateSmoother_Take_helper(t *testing.T, tryerGetter getTestTryer) {
	ctx := context.Background()
	name := fmt.Sprintf(" (%s)", t.Name())

	tests := []struct {
		name      string
		rps       int
		targerRPS int
		count     int
		duration  time.Duration
		calls     int
	}{
		{
			name:      "basic rate limiting RPS" + name,
			rps:       100,
			targerRPS: 100,
			count:     1,
			duration:  time.Second * 2,
			calls:     150 * 2, // Slightly more calls than possible in the duration
		},
		{
			name:      "high rate RPS" + name,
			rps:       1000,
			targerRPS: 1000,
			count:     1,
			duration:  time.Second * 5,
			calls:     1500 * 5,
		},
		{
			name:      "multiple tokens at once" + name,
			rps:       1000,
			targerRPS: 1000,
			count:     2,
			duration:  time.Second * 2,
			calls:     1500 * 2,
		},
		{
			name:      "RPS lower than target" + name,
			rps:       1000,
			targerRPS: 500,
			count:     2,
			duration:  time.Second * 2,
			calls:     500 * 2,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tryer := tryerGetter(tt.rps)
			smoother, err := NewRateSmoother(tryer, WithTimeout(tt.duration))
			require.NoError(t, err)

			start := time.Now()
			successfulCalls := 0

			// Send requests faster than the rate limit
			for i := 0; i < tt.calls/tt.count; i++ {
				_, err := smoother.Take(ctx, tt.count)
				require.NoError(t, err)

				if time.Since(start) >= tt.duration {
					break
				}

				successfulCalls++
			}

			actualRPS := float64(successfulCalls) / tt.duration.Seconds()
			expectedRPS := float64(tt.targerRPS) / float64(tt.count)

			// Allow for small timing variations ±152%
			marginOfError := 0.15
			assert.InDelta(t, expectedRPS, actualRPS, expectedRPS*marginOfError,
				"Expected rps: %.2f, Actual rps: %.2f (margin: ±%.0f%%)", expectedRPS, actualRPS, marginOfError*100)
		})
	}
}

func TestRateSmoother_ContextCancellation(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		testRateSmoother_ContextCancellation_Helper(t, setupTestLocalTryer(t))
	})
	t.Run("redis_rate", func(t *testing.T) {
		testRateSmoother_ContextCancellation_Helper(t, setupThrottledTryer(t))
	})
	t.Run("redis_rate", func(t *testing.T) {
		testRateSmoother_ContextCancellation_Helper(t, setupRedisRateTryer(t))
	})
}

func testRateSmoother_ContextCancellation_Helper(t *testing.T, tryerGetter getTestTryer) {
	tryer := tryerGetter(1)
	smoother, err := NewRateSmoother(tryer) // 1 RPS for easy timing
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())

	// First take should succeed immediately
	_, err = smoother.Take(ctx, 1)
	require.NoError(t, err, "name: %s", t.Name())

	// Start a goroutine that will cancel the context shortly
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()

	// This take should be blocked and then cancelled
	start := time.Now()
	for time.Since(start) < time.Second {
		_, err = smoother.Take(ctx, 1)
		if err != nil {
			break
		}
	}
	assert.ErrorIs(t, err, context.Canceled, "name: %s", t.Name())
}

func TestRateSmoother_Concurrency(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		testRateSmoother_Concurrency_Helper(t, setupTestLocalTryer(t))
	})
	t.Run("redis_rate", func(t *testing.T) {
		testRateSmoother_Concurrency_Helper(t, setupThrottledTryer(t))
	})
	t.Run("redis_rate", func(t *testing.T) {
		testRateSmoother_Concurrency_Helper(t, setupRedisRateTryer(t))
	})
}

func testRateSmoother_Concurrency_Helper(t *testing.T, tryerGetter getTestTryer) {
	const (
		multi      = 5
		rps        = 1000
		interval   = time.Second * multi
		goroutines = 100
	)

	var (
		ctx       = context.Background()
		rateTryer = tryerGetter(rps)

		start              = time.Now()
		wg                 sync.WaitGroup
		actualCalls        int64
		minDelay, maxDelay time.Duration
		muDelay            sync.Mutex
	)

	smoother, err := NewRateSmoother(rateTryer)
	require.NoError(t, err)

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				delay, err := smoother.Take(ctx, 1)
				require.NoError(t, err)

				muDelay.Lock()
				actualCalls++

				if delay > 0 {
					if minDelay == 0 || delay < minDelay {
						minDelay = delay
					}

					if delay > maxDelay {
						maxDelay = delay
					}
				}

				muDelay.Unlock()

				if time.Since(start) >= interval {
					break
				}
			}
		}()
	}
	wg.Wait()

	actualRate := float64(actualCalls) / interval.Seconds()
	expectedRate := float64(rps)

	// Allow for small timing variations ±10%
	marginOfError := 0.1
	assert.InDelta(t, expectedRate, actualRate, expectedRate*marginOfError,
		"MinDelay: %s, MaxDelay: %s, Expected rate: %.2f, Actual rate: %.2f (margin: ±%.0f%%). Name: %s",
		minDelay, maxDelay, expectedRate, actualRate, marginOfError*100, t.Name())
}
