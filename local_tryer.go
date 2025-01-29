package smoother

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LocalTryer implements a simple rate limiter.
// It allows n requests per interval and return false if the request is not allowed.
type LocalTryer struct {
	mu          sync.Mutex
	lastAllowed time.Time
	minInterval time.Duration
}

var _ Tryer = (*LocalTryer)(nil)

// NewLocalTryer creates a new LocalTryer instance.
func NewLocalTryer(rps int) (*LocalTryer, error) {
	if rps <= 0 {
		return nil, fmt.Errorf("NewLocalTryer: invalid rps %d", rps)
	}

	return &LocalTryer{
		minInterval: time.Second / time.Duration(rps),
	}, nil
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
