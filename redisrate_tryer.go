package smoother

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/n-r-w/smoother/redisrate"
	"github.com/redis/go-redis/v9"
)

// RedisRateTryerOption is a function that configures a RedisRateTryer.
type RedisRateTryerOption func(*RedisRateTryer)

// WithRedisRateTryerBurstFromRPSFunc sets the burst from rps function.
func WithRedisRateTryerBurstFromRPSFunc(burstFromRPSFunc BurstFromRPSFunc) RedisRateTryerOption {
	return func(t *RedisRateTryer) {
		t.burstFromRPSFunc = burstFromRPSFunc
	}
}

// RedisRateTryer implements a rate limiter using Redis and Redis Rate.
type RedisRateTryer struct {
	redisLimiter *redisrate.Limiter
	key          string

	rps              float64
	burst            float64
	burstFromRPSFunc BurstFromRPSFunc

	mu sync.RWMutex
}

var _ ITryer = (*RedisRateTryer)(nil)

// NewRedisRateTryer creates a new RedisTryer.
func NewRedisRateTryer(
	redisClient redis.UniversalClient, key string, rps float64, opts ...RedisRateTryerOption,
) (*RedisRateTryer, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("NewRedisRateTryer: redisClient is nil")
	}

	if key == "" {
		return nil, fmt.Errorf("NewRedisRateTryer: key is empty")
	}

	if rps <= 0 {
		return nil, fmt.Errorf("NewRedisRateTryer: invalid rps %f", rps)
	}

	redisLimiter := redisrate.NewLimiter(redisClient)

	t := &RedisRateTryer{
		redisLimiter: redisLimiter,
		key:          key,
	}

	// Set initial RPS
	t.rps = rps

	// Set default burst function
	// Store the function with its concrete type to avoid type assertion issues
	t.burstFromRPSFunc = DefaultBurstFromRPS

	for _, opt := range opts {
		opt(t)
	}

	if err := t.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewRedisRateTryer: %w", err)
	}

	// Calculate initial burst value
	t.burst = t.burstFromRPSFunc(rps)

	return t, nil
}

func (r *RedisRateTryer) validateOptions() error {
	// nothing to validate for now
	return nil
}

// TryTake attempts to take n requests.
// If the request is allowed, it returns true and zero duration.
// Otherwise, it returns false and interval to wait before next request.
func (r *RedisRateTryer) TryTake(ctx context.Context, count float64) (bool, time.Duration, error) {
	// Get current RPS and burst values
	r.mu.RLock()
	rps := r.rps
	burst := r.burst
	r.mu.RUnlock()

	// Burst should be at least the number of requests, otherwise it will hang
	minBurst := float64(count) + 1
	if burst < minBurst {
		burst = minBurst
	}

	// Create limit for this request
	limit := redisrate.Limit{
		Rate:   rps,
		Period: time.Second,
		Burst:  burst,
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

// SetRate updates the rate limit's requests per second.
// This change will take effect on the next TryTake call.
func (r *RedisRateTryer) SetRate(rps float64) error {
	if rps <= 0 {
		return fmt.Errorf("RedisRateTryer.SetRate: invalid rps %f", rps)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Update RPS atomically
	r.rps = rps

	// Calculate and update burst atomically
	r.burst = r.burstFromRPSFunc(rps)

	return nil
}

// SetMultiplier updates the burst multiplier by updating the burst function.
// This change will take effect on the next TryTake call.
func (r *RedisRateTryer) SetMultiplier(multiplier float64) error {
	if multiplier <= 0 {
		return fmt.Errorf("RedisRateTryer.SetMultiplier: invalid multiplier %f", multiplier)
	}

	// Create a new burst function that applies the multiplier
	newFunc := func(rps float64) float64 {
		return float64(rps) * multiplier
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Store the new function
	r.burstFromRPSFunc = newFunc

	// Update the burst value with the new function
	r.burst = newFunc(r.rps)

	return nil
}

// GetRate returns the current rate limit in requests per second.
func (r *RedisRateTryer) GetRate() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.rps
}

// GetMultiplier returns the current multiplier value.
func (r *RedisRateTryer) GetMultiplier() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.burst / r.rps
}

// GetBurst returns the current burst value.
func (r *RedisRateTryer) GetBurst() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.burst
}

// Reset resets the Tryer to its initial state.
func (r *RedisRateTryer) Reset(ctx context.Context) error {
	if err := r.redisLimiter.Reset(ctx, r.key); err != nil {
		return fmt.Errorf("RedisRateTryer.Reset: %w", err)
	}
	return nil
}
