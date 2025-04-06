package smoother

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/redis/go-redis/v9"
)

// RedisRateTryerOption is a function that configures a RedisRateTryer.
type RedisRateTryerOption func(*RedisRateTryer)

// WithRedisRateTryerBurstFromRPSFunc sets the burst from rps function.
func WithRedisRateTryerBurstFromRPSFunc(burstFromRPSFunc BurstFromRPSFunc) RedisRateTryerOption {
	return func(t *RedisRateTryer) {
		t.burstFromRPSFunc.Store(burstFromRPSFunc)
	}
}

// RedisRateTryer implements a rate limiter using Redis and Redis Rate.
type RedisRateTryer struct {
	redisLimiter     *redis_rate.Limiter
	key              string
	rps              atomic.Int64 // atomic RPS value
	burst            atomic.Int64 // atomic burst value
	burstFromRPSFunc atomic.Value // stores BurstFromRPSFunc
}

var _ ITryer = (*RedisRateTryer)(nil)

// NewRedisRateTryer creates a new RedisTryer.
func NewRedisRateTryer(
	redisClient redis.UniversalClient, key string, rps int, opts ...RedisRateTryerOption,
) (*RedisRateTryer, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("NewRedisRateTryer: redisClient is nil")
	}

	if key == "" {
		return nil, fmt.Errorf("NewRedisRateTryer: key is empty")
	}

	if rps <= 0 {
		return nil, fmt.Errorf("NewRedisRateTryer: invalid rps %d", rps)
	}

	redisLimiter := redis_rate.NewLimiter(redisClient)

	t := &RedisRateTryer{
		redisLimiter: redisLimiter,
		key:          key,
	}

	// Set initial RPS
	t.rps.Store(int64(rps))

	// Set default burst function
	t.burstFromRPSFunc.Store(DefaultBurstFromRPS)

	for _, opt := range opts {
		opt(t)
	}

	if err := t.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewRedisRateTryer: %w", err)
	}

	// Calculate initial burst value
	burstFunc := t.burstFromRPSFunc.Load().(BurstFromRPSFunc)
	t.burst.Store(int64(burstFunc(rps)))

	return t, nil
}

func (r *RedisRateTryer) validateOptions() error {
	// nothing to validate for now
	return nil
}

// TryTake attempts to take n requests.
// If the request is allowed, it returns true and zero duration.
// Otherwise, it returns false and interval to wait before next request.
func (r *RedisRateTryer) TryTake(ctx context.Context, count int) (bool, time.Duration, error) {
	// Get current RPS and burst values atomically
	rps := r.rps.Load()
	burst := r.burst.Load()

	// Burst should be at least the number of requests, otherwise it will hang
	minBurst := int64(count + 1)
	if burst < minBurst {
		burst = minBurst
	}

	// Create limit for this request
	limit := redis_rate.Limit{
		Rate:   int(rps),
		Period: time.Second,
		Burst:  int(burst),
	}

	res, err := r.redisLimiter.AllowN(ctx, r.key, limit, count)
	if err != nil {
		return false, 0, fmt.Errorf("redis: %w", err)
	}

	if res.RetryAfter <= 0 {
		return true, 0, nil
	}

	return false, res.RetryAfter, nil
}

// SetRPS updates the rate limit's requests per second.
// This change will take effect on the next TryTake call.
func (r *RedisRateTryer) SetRPS(rps int) error {
	if rps <= 0 {
		return fmt.Errorf("RedisRateTryer.SetRPS: invalid rps %d", rps)
	}

	// Update RPS atomically
	r.rps.Store(int64(rps))

	// Calculate and update burst atomically
	burstFunc := r.burstFromRPSFunc.Load().(BurstFromRPSFunc)
	r.burst.Store(int64(burstFunc(rps)))

	return nil
}

// SetMultiplier updates the burst multiplier by updating the burst function.
// This change will take effect on the next TryTake call.
func (r *RedisRateTryer) SetMultiplier(multiplier float64) error {
	if multiplier <= 0 {
		return fmt.Errorf("RedisRateTryer.SetMultiplier: invalid multiplier %f", multiplier)
	}

	// Get the current burst function
	currentBurstFunc := r.burstFromRPSFunc.Load().(BurstFromRPSFunc)

	// Create a new burst function that applies the multiplier
	newFunc := func(rps int) int {
		return int(float64(currentBurstFunc(rps)) * multiplier)
	}

	// Store the new function
	r.burstFromRPSFunc.Store(newFunc)

	// Update the burst value with the new function
	currentRPS := int(r.rps.Load())
	r.burst.Store(int64(newFunc(currentRPS)))

	return nil
}

// GetRPS returns the current rate limit in requests per second.
func (r *RedisRateTryer) GetRPS() int {
	return int(r.rps.Load())
}

// GetBurst returns the current burst value.
func (r *RedisRateTryer) GetBurst() int {
	return int(r.burst.Load())
}

// Reset resets the Tryer to its initial state.
func (r *RedisRateTryer) Reset(ctx context.Context) error {
	if err := r.redisLimiter.Reset(ctx, r.key); err != nil {
		return fmt.Errorf("RedisRateTryer.Reset: %w", err)
	}
	return nil
}
