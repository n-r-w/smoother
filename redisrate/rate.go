package redisrate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisPrefix = "rate:"

type rediser interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	EvalSha(ctx context.Context, sha1 string, keys []string, args ...any) *redis.Cmd
	ScriptExists(ctx context.Context, hashes ...string) *redis.BoolSliceCmd
	ScriptLoad(ctx context.Context, script string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd

	EvalRO(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	EvalShaRO(ctx context.Context, sha1 string, keys []string, args ...any) *redis.Cmd
}

// Limit defines the maximum frequency of some events.
type Limit struct {
	Rate   float64       // The rate of events per Period.
	Burst  float64       // The maximum burst size.
	Period time.Duration // The period for the rate.
}

// String returns a string representation of the Limit.
func (l Limit) String() string {
	return fmt.Sprintf("%g req/%s (burst %g)", l.Rate, fmtDur(l.Period), l.Burst)
}

// IsZero reports whether the limit is the zero value.
func (l Limit) IsZero() bool {
	return l == Limit{}
}

func fmtDur(d time.Duration) string {
	switch d { //nolint:exhaustive // ok
	case time.Second:
		return "s"
	case time.Minute:
		return "m"
	case time.Hour:
		return "h"
	}
	return d.String()
}

// PerSecond returns a Limit that allows rate events per second.
func PerSecond(rate float64) Limit {
	return Limit{
		Rate:   rate,
		Period: time.Second,
		Burst:  rate, // Default burst to rate for simple cases.
	}
}

// PerMinute returns a Limit that allows rate events per minute.
func PerMinute(rate float64) Limit {
	return Limit{
		Rate:   rate,
		Period: time.Minute,
		Burst:  rate, // Default burst to rate for simple cases.
	}
}

// PerHour returns a Limit that allows rate events per hour.
func PerHour(rate float64) Limit {
	return Limit{
		Rate:   rate,
		Period: time.Hour,
		Burst:  rate, // Default burst to rate for simple cases.
	}
}

// ------------------------------------------------------------------------------

// Limiter controls how frequently events are allowed to happen.
type Limiter struct {
	rdb rediser
}

// NewLimiter returns a new Limiter.
func NewLimiter(rdb rediser) *Limiter {
	return &Limiter{
		rdb: rdb,
	}
}

// Allow is a shortcut for AllowN(ctx, key, limit, 1).
func (l Limiter) Allow(ctx context.Context, key string, limit Limit) (*Result, error) {
	return l.AllowN(ctx, key, limit, 1)
}

// AllowN reports whether n events may happen at time now.
func (l Limiter) AllowN(
	ctx context.Context,
	key string,
	limit Limit,
	n int,
) (*Result, error) {
	values := []any{limit.Burst, limit.Rate, limit.Period.Seconds(), n}
	v, err := allowN.Run(ctx, l.rdb, []string{redisPrefix + key}, values...).Result()
	if err != nil {
		return nil, err
	}

	results, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("redis: unexpected result type: %T", v)
	}
	const expectedResults = 4
	if len(results) != expectedResults {
		return nil, fmt.Errorf("redis: unexpected result length: %d", len(results))
	}

	allowed, err := parseRedisResultFloat(results[0])
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse allowed: %w", err)
	}

	remaining, err := parseRedisResultFloat(results[1])
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse remaining: %w", err)
	}

	retryAfterStr, ok := results[2].(string)
	if !ok {
		return nil, fmt.Errorf("redis: unexpected type for retryAfter: %T", results[2])
	}
	retryAfter, err := strconv.ParseFloat(retryAfterStr, 64)
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse retryAfter: %w", err)
	}

	resetAfterStr, ok := results[3].(string)
	if !ok {
		return nil, fmt.Errorf("redis: unexpected type for resetAfter: %T", results[3])
	}
	resetAfter, err := strconv.ParseFloat(resetAfterStr, 64)
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse resetAfter: %w", err)
	}

	res := &Result{
		Limit:      limit,
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: dur(retryAfter), // dur function already handles float64.
		ResetAfter: dur(resetAfter), // dur function already handles float64.
	}
	return res, nil
}

// AllowAtMost reports whether at most n events may happen at time now.
// It returns number of allowed events that is less than or equal to n.
func (l Limiter) AllowAtMost(
	ctx context.Context,
	key string,
	limit Limit,
	n int,
) (*Result, error) {
	values := []any{limit.Burst, limit.Rate, limit.Period.Seconds(), n}
	v, err := allowAtMost.Run(ctx, l.rdb, []string{redisPrefix + key}, values...).Result()
	if err != nil {
		return nil, err
	}

	results, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("redis: unexpected result type: %T", v)
	}
	const expectedResults = 4
	if len(results) != expectedResults {
		return nil, fmt.Errorf("redis: unexpected result length: %d", len(results))
	}

	allowed, err := parseRedisResultFloat(results[0])
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse allowed: %w", err)
	}

	remaining, err := parseRedisResultFloat(results[1])
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse remaining: %w", err)
	}

	retryAfterStr, ok := results[2].(string)
	if !ok {
		return nil, fmt.Errorf("redis: unexpected type for retryAfter: %T", results[2])
	}
	retryAfter, err := strconv.ParseFloat(retryAfterStr, 64)
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse retryAfter: %w", err)
	}

	resetAfterStr, ok := results[3].(string)
	if !ok {
		return nil, fmt.Errorf("redis: unexpected type for resetAfter: %T", results[3])
	}
	resetAfter, err := strconv.ParseFloat(resetAfterStr, 64)
	if err != nil {
		return nil, fmt.Errorf("redis: failed to parse resetAfter: %w", err)
	}

	res := &Result{
		Limit:      limit,
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: dur(retryAfter), // dur function already handles float64.
		ResetAfter: dur(resetAfter), // dur function already handles float64.
	}
	return res, nil
}

// Reset gets a key and reset all limitations and previous usages
func (l *Limiter) Reset(ctx context.Context, key string) error {
	return l.rdb.Del(ctx, redisPrefix+key).Err()
}

func dur(f float64) time.Duration {
	if f == -1 {
		return -1
	}
	return time.Duration(f * float64(time.Second))
}

// Result contains information about whether a RateLimiter allowed an event to happen.
type Result struct {
	// Limit is the limit that was used to obtain this result.
	Limit Limit

	// Allowed is the number of events that may happen at time now.
	Allowed float64

	// Remaining is the maximum number of requests that could be
	// permitted instantaneously for this key given the current
	// state. For example, if a rate limiter allows 10 requests per
	// second and has already received 6 requests for this key this
	// second, Remaining would be 4.
	Remaining float64

	// RetryAfter is the time until the next request will be permitted.
	// It should be -1 unless the rate limit has been exceeded.
	RetryAfter time.Duration

	// ResetAfter is the time until the RateLimiter returns to its
	// initial state for a given key. For example, if a rate limiter
	// manages requests per second and received one request 200ms ago,
	// Reset would return 800ms. You can also think of this as the time
	// until Limit and Remaining will be equal.
	ResetAfter time.Duration
}

// parseRedisResultFloat handles parsing numeric results from Redis Lua scripts,
// which might return int64 or potentially other numeric types.
func parseRedisResultFloat(v any) (float64, error) {
	switch val := v.(type) {
	case int64:
		return float64(val), nil
	case float64:
		return val, nil
	case string: // Lua might return numbers as strings in some cases.
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, fmt.Errorf("could not parse string %q as float: %w", val, err)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("unexpected type %T for numeric result", v)
	}
}
