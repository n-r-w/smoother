package smoother

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/throttled/throttled/v2"
	"github.com/throttled/throttled/v2/store/goredisstore.v9"
)

// RedisThrottledTryerOption is a function that configures a RedisThrottledTryer.
type RedisThrottledTryerOption func(*RedisThrottledTryer)

// WithRedisThrottledTryerBurstFromRPSFunc sets the burst from rps function.
func WithRedisThrottledTryerBurstFromRPSFunc(burstFromRPSFunc BurstFromRPSFunc) RedisThrottledTryerOption {
	return func(r *RedisThrottledTryer) {
		r.burstFromRPSFunc = burstFromRPSFunc
	}
}

// RedisThrottledTryer implements a rate limiter using Redis and throttled limiter.
type RedisThrottledTryer struct {
	mu               sync.Mutex
	rateLimiter      *throttled.GCRARateLimiterCtx
	reseter          func() (*throttled.GCRARateLimiterCtx, error)
	key              string
	burstFromRPSFunc BurstFromRPSFunc
}

var _ Tryer = (*RedisThrottledTryer)(nil)

// NewRedisThrottledTryer creates a new RedisThrottledTryer.
func NewRedisThrottledTryer(
	redisClient redis.UniversalClient, group int, key string, rps uint32, opts ...RedisThrottledTryerOption,
) (*RedisThrottledTryer, error) {
	store, err := goredisstore.NewCtx(redisClient, "throttled:")
	if err != nil {
		return nil, fmt.Errorf("goredisstore.NewCtx: %w", err)
	}

	t := &RedisThrottledTryer{
		key:              fmt.Sprintf("%s:%d", key, group),
		burstFromRPSFunc: DefaultBurstFromRPS,
	}

	for _, opt := range opts {
		opt(t)
	}

	reseter := func() (*throttled.GCRARateLimiterCtx, error) {
		rateLimiter, err := throttled.NewGCRARateLimiterCtx(store,
			throttled.RateQuota{
				MaxRate:  throttled.PerSec(int(rps)),
				MaxBurst: t.burstFromRPSFunc(int(rps)),
			})
		if err != nil {
			return nil, fmt.Errorf("hrottled.NewGCRARateLimiterCtx: %w", err)
		}

		return rateLimiter, nil
	}

	rateLimiter, err := reseter()
	if err != nil {
		return nil, err
	}

	t.rateLimiter = rateLimiter
	t.reseter = reseter

	return t, nil
}

// TryTake attempts to take n requests.
// If the request is allowed, it returns true and zero duration.
// Otherwise, it returns false and interval to wait before next request.
func (r *RedisThrottledTryer) TryTake(ctx context.Context, count uint32) (bool, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	limited, res, err := r.rateLimiter.RateLimitCtx(ctx, r.key, int(count))
	if err != nil {
		// throttled does not have a bug handling cancellation of the context,
		// this is a workaround
		if strings.Contains(err.Error(), "strconv.ParseInt: parsing") {
			return false, 0, context.Canceled
		}

		return false, 0, fmt.Errorf("RedisThrottledTryer.TryTake : %w", err)
	}

	if !limited {
		return true, 0, nil
	}

	return false, res.RetryAfter, nil
}

// Reset resets the Tryer to its initial state.
func (r *RedisThrottledTryer) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rl, err := r.reseter()
	if err != nil {
		return err
	}
	r.rateLimiter = rl
	return nil
}
