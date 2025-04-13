package redisrate

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func rateLimiter() *Limiter {
	ring := redis.NewRing(&redis.RingOptions{
		Addrs: map[string]string{"server0": ":26379"},
	})
	if err := ring.FlushDB(context.TODO()).Err(); err != nil {
		panic(err)
	}
	return NewLimiter(ring)
}

func TestAllow(t *testing.T) {
	ctx := context.Background()

	l := rateLimiter()

	limit := PerSecond(10.0)
	require.Equal(t, limit.String(), "10 req/s (burst 10)") // String format uses %g, which might not show .0
	require.False(t, limit.IsZero())

	res, err := l.Allow(ctx, "test_id", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))

	err = l.Reset(ctx, "test_id")
	require.Nil(t, err)
	res, err = l.Allow(ctx, "test_id", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowN(ctx, "test_id", limit, 2)
	require.Nil(t, err)
	require.InDelta(t, 2.0, res.Allowed, 0.01)
	require.InDelta(t, 7.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 300*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowN(ctx, "test_id", limit, 7)
	require.Nil(t, err)
	require.InDelta(t, 7.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 999*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowN(ctx, "test_id", limit, 1000)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.InDelta(t, res.RetryAfter, 99*time.Second, float64(time.Second))
	require.InDelta(t, res.ResetAfter, 999*time.Millisecond, float64(10*time.Millisecond))
}

func TestAllowN_IncrementZero(t *testing.T) {
	ctx := context.Background()
	l := rateLimiter()
	limit := PerSecond(10.0)

	// Check for a row that's not there
	res, err := l.AllowN(ctx, "test_id", limit, 0)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 10.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.Equal(t, res.ResetAfter, time.Duration(0))

	// Now increment it
	res, err = l.Allow(ctx, "test_id", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))

	// Peek again
	res, err = l.AllowN(ctx, "test_id", limit, 0)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))
}

func TestRetryAfter(t *testing.T) {
	limit := Limit{
		Rate:   1.0,
		Period: time.Millisecond,
		Burst:  1.0,
	}

	ctx := context.Background()
	l := rateLimiter()

	for range 1000 {
		res, err := l.Allow(ctx, "test_id", limit)
		require.Nil(t, err)

		if res.Allowed > 0.0 {
			continue
		}

		require.LessOrEqual(t, int64(res.RetryAfter), int64(time.Millisecond))
	}
}

func TestAllowAtMost(t *testing.T) {
	ctx := context.Background()

	l := rateLimiter()
	limit := PerSecond(10.0)

	res, err := l.Allow(ctx, "test_id", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowAtMost(ctx, "test_id", limit, 2)
	require.Nil(t, err)
	require.InDelta(t, 2.0, res.Allowed, 0.01)
	require.InDelta(t, 7.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 300*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowN(ctx, "test_id", limit, 0)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 7.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 300*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowAtMost(ctx, "test_id", limit, 10)
	require.Nil(t, err)
	// AllowAtMost allows up to the remaining limit if n > remaining.
	require.InDelta(t, 7.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 999*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowN(ctx, "test_id", limit, 0)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 999*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowAtMost(ctx, "test_id", limit, 1000)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.InDelta(t, res.RetryAfter, 99*time.Millisecond, float64(10*time.Millisecond))
	require.InDelta(t, res.ResetAfter, 999*time.Millisecond, float64(10*time.Millisecond))

	res, err = l.AllowN(ctx, "test_id", limit, 1000)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.InDelta(t, res.RetryAfter, 99*time.Second, float64(time.Second))
	require.InDelta(t, res.ResetAfter, 999*time.Millisecond, float64(10*time.Millisecond))
}

func TestAllowAtMost_IncrementZero(t *testing.T) {
	ctx := context.Background()
	l := rateLimiter()
	limit := PerSecond(10.0)

	// Check for a row that isn't there
	res, err := l.AllowAtMost(ctx, "test_id", limit, 0)
	require.Nil(t, err)
	require.Equal(t, 0.0, res.Allowed)
	require.Equal(t, 10.0, res.Remaining)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.Equal(t, res.ResetAfter, time.Duration(0))

	// Now increment it
	res, err = l.Allow(ctx, "test_id", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))

	// Peek again
	res, err = l.AllowAtMost(ctx, "test_id", limit, 0)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 9.0, res.Remaining, 0.01)
	require.Equal(t, res.RetryAfter, time.Duration(-1))
	require.InDelta(t, res.ResetAfter, 100*time.Millisecond, float64(10*time.Millisecond))
}

func BenchmarkAllow(b *testing.B) {
	ctx := context.Background()
	l := rateLimiter()
	limit := PerSecond(1e6) // 1e6 is already a float literal

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			res, err := l.Allow(ctx, "foo", limit)
			if err != nil {
				b.Fatal(err)
			}
			if res.Allowed == 0.0 {
				panic("not reached")
			}
		}
	})
}

func BenchmarkAllowAtMost(b *testing.B) {
	ctx := context.Background()
	l := rateLimiter()
	limit := PerSecond(1e6) // 1e6 is already a float literal

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			res, err := l.AllowAtMost(ctx, "foo", limit, 1)
			if err != nil {
				b.Fatal(err)
			}
			if res.Allowed == 0.0 {
				panic("not reached")
			}
		}
	})
}

func TestAllow_FractionalRates(t *testing.T) {
	ctx := context.Background()
	l := rateLimiter()

	// Test with non-integer rate (2.4 requests per second)
	limit := Limit{
		Rate:   2.4,
		Period: 1 * time.Second,
		Burst:  2.4,
	}

	// First request should be allowed
	res, err := l.Allow(ctx, "fractional_test", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 1.4, res.Remaining, 0.01)
	require.Equal(t, time.Duration(-1), res.RetryAfter)

	// Second request immediately after should also be allowed
	res, err = l.Allow(ctx, "fractional_test", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 0.4, res.Remaining, 0.01)
	require.Equal(t, time.Duration(-1), res.RetryAfter)

	// Third request immediately after should be rejected (not enough tokens)
	res, err = l.Allow(ctx, "fractional_test", limit)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 0.4, res.Remaining, 0.01) // Should still have 0.4 tokens remaining
	require.InDelta(t, 250*time.Millisecond, res.RetryAfter, float64(100*time.Millisecond))

	// After waiting 250ms, should have accumulated 0.6 more tokens (total 1.0)
	time.Sleep(250 * time.Millisecond)
	res, err = l.Allow(ctx, "fractional_test", limit)
	require.Nil(t, err)
	require.InDelta(t, 1.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.Equal(t, time.Duration(-1), res.RetryAfter)

	// Another request immediately after should be rejected
	res, err = l.Allow(ctx, "fractional_test", limit)
	require.Nil(t, err)
	require.InDelta(t, 0.0, res.Allowed, 0.01)
	require.InDelta(t, 0.0, res.Remaining, 0.01)
	require.InDelta(t, 417*time.Millisecond, res.RetryAfter, float64(100*time.Millisecond)) // ~417ms to accumulate 1 token at 2.4 tokens/sec
}
