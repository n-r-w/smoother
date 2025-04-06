package smoother

import (
	"context"
	"time"
)

// ITryer is an interface that responsible for trying to take n requests.
type ITryer interface {
	// TryTake attempts to take n requests.
	// If the request is allowed, it returns true and zero duration.
	// Otherwise, it returns false and interval to wait before next request.
	TryTake(ctx context.Context, count int) (allowed bool, waitTime time.Duration, err error)
}

// Interfaces IRateSmoother and IRateManager are created at the place
// of implementation for convenience of use in external packages.

// IRateSmoother is an interface that responsible for smoothing the rate of requests.
type IRateSmoother interface {
	// Take blocks to ensure that the time spent between multiple Take calls is on average per/rate.
	// The count parameter specifies how many tokens to take at once.
	// It returns the time at which function waits for allowance.
	Take(ctx context.Context, count int) (time.Duration, error)
	// Start starts the smoother.
	Start()
	// Stop stops the smoother.
	Stop()
}

// IRateManager is an interface that responsible for changing the rate of requests
// and getting the current rate.
type IRateManager interface {
	// SetRate updates only the RPS (requests per second) value of the LocalTryer.
	SetRate(rps int) error
	// SetMultiplier updates only the multiplier value of the LocalTryer.
	SetMultiplier(multiplier float64) error
	// GetRate returns the current rate limit in requests per second.
	GetRate() int
	// GetMultiplier returns the current multiplier value.
	GetMultiplier() float64
}
