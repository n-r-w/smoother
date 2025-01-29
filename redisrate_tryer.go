package smoother

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
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
	redisLimiter     *redis_rate.Limiter
	limit            redis_rate.Limit
	key              string
	burstFromRPSFunc BurstFromRPSFunc
}

var _ Tryer = (*RedisRateTryer)(nil)

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
		limit: redis_rate.Limit{
			Rate:   rps,
			Period: time.Second,
		},
		key:              key,
		burstFromRPSFunc: DefaultBurstFromRPS,
	}

	for _, opt := range opts {
		opt(t)
	}

	if err := t.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewRedisRateTryer: %w", err)
	}

	t.limit.Burst = t.burstFromRPSFunc(rps)

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
	// Burst should be at least the number of requests, otherwise it will hang
	limit := r.limit
	minBurst := count + 1
	if limit.Burst < minBurst {
		limit.Burst = minBurst
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

// Reset resets the Tryer to its initial state.
func (r *RedisRateTryer) Reset(ctx context.Context) error {
	if err := r.redisLimiter.Reset(ctx, r.key); err != nil {
		return fmt.Errorf("RedisRateTryer.Reset: %w", err)
	}
	return nil
}
