package throttler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/semaphore"
)

// Option is a function that configures a Trottler.
type Option func(*Throttler)

// WithTimeout sets the timeout for requests to rate limiter.
// default is 0. 0 means no timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(t *Throttler) {
		t.timeout = timeout
	}
}

// Throttler implements back pressure pattern.
type Throttler struct {
	sem     *semaphore.Weighted
	timeout time.Duration
}

// New creates a new Trottler instance.
func New(maxConcurrency int, opt ...Option) (*Throttler, error) {
	if maxConcurrency <= 0 {
		return nil, errors.New("maxConcurrency must be positive")
	}

	t := &Throttler{
		sem: semaphore.NewWeighted(int64(maxConcurrency)),
	}

	for _, o := range opt {
		o(t)
	}

	if t.timeout < 0 {
		return nil, errors.New("timeout must be positive")
	}

	return t, nil
}

// Execute will be the core method controlling request flow.
func (t *Throttler) Execute(
	ctx context.Context,
	operation func(ctx context.Context) error,
) error {
	ctxOp := ctx
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctxOp, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	if err := t.sem.Acquire(ctxOp, 1); err != nil {
		return fmt.Errorf("failed to acquire semaphore: %w", err)
	}
	defer t.sem.Release(1)

	return operation(ctxOp)
}
