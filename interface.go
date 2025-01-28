package smoother

import (
	"context"
	"time"
)

// Tryer is an interface that responsible for trying to take n requests.
type Tryer interface {
	// TryTake attempts to take n requests.
	// If the request is allowed, it returns true and zero duration.
	// Otherwise, it returns false and interval to wait before next request.
	TryTake(ctx context.Context, count uint32) (allowed bool, waitTime time.Duration, err error)
}
