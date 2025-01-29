package smoother

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultTimeout is the default timeout for requests to rate limiter.
const DefaultTimeout = time.Second

// Option is a function that configures a RateSmoother.
type Option func(*RateSmoother)

// WithTimeout sets the timeout for requests to rate limiter.
func WithTimeout(timeout time.Duration) Option {
	return func(r *RateSmoother) {
		r.timeout = timeout
	}
}

// RateSmoother implements a rate limiter that blocks to ensure that the time spent between multiple.
type RateSmoother struct {
	mu      sync.Mutex
	timer   *time.Timer
	tryer   Tryer
	timeout time.Duration
}

// NewRateSmoother creates a new RateSmoother instance.
func NewRateSmoother(tryer Tryer, opts ...Option) (*RateSmoother, error) {
	if tryer == nil {
		return nil, fmt.Errorf("NewRateSmoother: nil tryer")
	}

	s := &RateSmoother{
		timer:   time.NewTimer(0),
		tryer:   tryer,
		timeout: DefaultTimeout,
	}

	for _, opt := range opts {
		opt(s)
	}

	if err := s.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewRateSmoother: %w", err)
	}

	return s, nil
}

func (r *RateSmoother) validateOptions() error {
	if r.timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	return nil
}

// Take blocks to ensure that the time spent between multiple Take calls is on average per/rate.
// The count parameter specifies how many tokens to take at once.
// It returns the time at which function waits for allowance.
func (r *RateSmoother) Take(ctx context.Context, count uint32) (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var (
		allowed  bool
		waitTime time.Duration
		err      error
		start    = time.Now()
	)
	for {
		ctxRequest, cancel := context.WithTimeout(ctx, r.timeout)
		allowed, waitTime, err = r.tryer.TryTake(ctxRequest, count)
		cancel()
		if err != nil {
			return time.Since(start), fmt.Errorf("tryer: %w", err)
		}

		if allowed {
			break
		}

		r.timer.Reset(waitTime)
		select {
		case <-ctx.Done():
			return time.Since(start), ctx.Err() // go 1.22+ don't need to drain the timer
		case <-r.timer.C:
			continue
		}
	}

	return time.Since(start), nil
}

// BurstFromRPSFunc is the function to calculate the burst from rps.
type BurstFromRPSFunc func(rps int) int

// DefaultBurstFromRPS calculates the empirical dependency of the burst,
// so that it does not freeze rps.
func DefaultBurstFromRPS(rps int) int {
	burst := rps / 500 //nolint:mnd // ok
	if burst < 1 {
		burst = 1
	}
	if burst > 100 { //nolint:mnd // ok
		burst = 100
	}
	return burst
}
