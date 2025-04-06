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

var _ ITryer = (*RedisThrottledTryer)(nil)

// NewRedisThrottledTryer creates a new RedisThrottledTryer.
func NewRedisThrottledTryer(
	redisClient redis.UniversalClient, key string, rps int, opts ...RedisThrottledTryerOption,
) (*RedisThrottledTryer, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("NewRedisThrottledTryer:redisClient is nil")
	}

	if key == "" {
		return nil, fmt.Errorf("NewRedisThrottledTryer: key is empty")
	}

	if rps <= 0 {
		return nil, fmt.Errorf("NewRedisThrottledTryer: invalid rps %d", rps)
	}

	store, err := goredisstore.NewCtx(redisClient, "throttled:")
	if err != nil {
		return nil, fmt.Errorf("NewRedisThrottledTryer, goredisstore.NewCtx: %w", err)
	}

	t := &RedisThrottledTryer{
		key:              key,
		burstFromRPSFunc: DefaultBurstFromRPS,
	}

	for _, opt := range opts {
		opt(t)
	}

	if err := t.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewRedisThrottledTryer: %w", err)
	}

	reseter := func() (*throttled.GCRARateLimiterCtx, error) {
		rateLimiter, err := throttled.NewGCRARateLimiterCtx(store,
			throttled.RateQuota{
				MaxRate:  throttled.PerSec(rps),
				MaxBurst: t.burstFromRPSFunc(rps),
			})
		if err != nil {
			return nil, fmt.Errorf("NewRedisThrottledTryer, throttled.NewGCRARateLimiterCtx: %w", err)
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

func (r *RedisThrottledTryer) validateOptions() error {
	// nothing to validate for now
	return nil
}

// TryTake attempts to take n requests.
// If the request is allowed, it returns true and zero duration.
// Otherwise, it returns false and interval to wait before next request.
func (r *RedisThrottledTryer) TryTake(ctx context.Context, count int) (bool, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	limited, res, err := r.rateLimiter.RateLimitCtx(ctx, r.key, count)
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
