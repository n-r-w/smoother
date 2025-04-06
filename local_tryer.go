package smoother

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LocalTryerOption is a function that configures a LocalTryer.
type LocalTryerOption func(*LocalTryer)

// WithMultiplier sets the multiplier for the rate limit.
// It allows to scale local traffic down by a factor of `multiplier`, compared to the
// overall traffic that would be allowed by a redis rate limiter with the same number of instances.
// Default is 1.
func WithMultiplier(multiplier float64) LocalTryerOption {
	return func(r *LocalTryer) {
		r.multiplier = multiplier
	}
}

// LocalTryer implements a simple rate limiter.
// It allows n requests per interval and return false if the request is not allowed.
type LocalTryer struct {
	mu          sync.RWMutex
	lastAllowed time.Time
	minInterval time.Duration

	multiplier float64
	rps        int
}

var _ Tryer = (*LocalTryer)(nil)

// NewLocalTryer creates a new LocalTryer instance.
func NewLocalTryer(rps int, opts ...LocalTryerOption) (*LocalTryer, error) {
	if rps <= 0 {
		return nil, fmt.Errorf("NewLocalTryer: invalid rps %d", rps)
	}

	r := &LocalTryer{
		multiplier: 1,
	}

	for _, opt := range opts {
		opt(r)
	}

	if r.multiplier <= 0 {
		return nil, fmt.Errorf("NewLocalTryer: multiplier must be positive %f", r.multiplier)
	}

	r.rps = rps
	r.minInterval = r.calculateMinInterval(rps)

	return r, nil
}

// TryTake attempts to take n requests.
// If the request is allowed, it returns true and zero duration.
// Otherwise, it returns false and interval to wait before next request.
func (r *LocalTryer) TryTake(_ context.Context, count int) (ok bool, duration time.Duration, err error) {
	if count < 0 {
		return false, 0, fmt.Errorf("TryTake: invalid count %d", count)
	}

	if count == 0 {
		return true, 0, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	start := time.Now()

	if r.lastAllowed.IsZero() {
		r.lastAllowed = start
		return true, 0, nil
	}

	nextAllowed := r.lastAllowed.Add(r.minInterval * time.Duration(count))
	if waitTime := nextAllowed.Sub(start); waitTime > 0 {
		return false, waitTime, nil
	}

	r.lastAllowed = nextAllowed
	return true, 0, nil
}

// Reset resets the Tryer to its initial state.
func (r *LocalTryer) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastAllowed = time.Time{}
	return nil
}

// SetRateLimit updates the rate limit of the LocalTryer.
// It takes a new RPS (requests per second) value and multiplier, then recalculates the minimum interval.
// Returns an error if the new RPS or multiplier is invalid.
func (r *LocalTryer) SetRateLimit(rps int, multiplier float64) error {
	if rps <= 0 {
		return fmt.Errorf("SetRateLimit: invalid rps %d", rps)
	}

	if multiplier <= 0 {
		return fmt.Errorf("SetRateLimit: multiplier must be positive %f", multiplier)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.rps = rps
	r.multiplier = multiplier
	r.minInterval = r.calculateMinInterval(rps)

	return nil
}

// GetRateLimit returns the current rate limit in requests per second.
func (r *LocalTryer) GetRateLimit() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	return r.rps
}

// GetMultiplier returns the current multiplier value.
func (r *LocalTryer) GetMultiplier() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	return r.multiplier
}

// calculateMinInterval calculates the minimum interval between requests based on RPS and multiplier.
func (r *LocalTryer) calculateMinInterval(rps int) time.Duration {
	return time.Second * time.Duration(r.multiplier) / time.Duration(rps)
}

// SetRPS updates only the RPS (requests per second) value of the LocalTryer.
// Returns an error if the new RPS is invalid.
func (r *LocalTryer) SetRPS(rps int) error {
	if rps <= 0 {
		return fmt.Errorf("SetRPS: invalid rps %d", rps)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.rps = rps
	r.minInterval = r.calculateMinInterval(rps)

	return nil
}

// SetMultiplier updates only the multiplier value of the LocalTryer.
// Returns an error if the new multiplier is invalid.
func (r *LocalTryer) SetMultiplier(multiplier float64) error {
	if multiplier <= 0 {
		return fmt.Errorf("SetMultiplier: multiplier must be positive %f", multiplier)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.multiplier = multiplier
	r.minInterval = r.calculateMinInterval(r.rps)

	return nil
}
