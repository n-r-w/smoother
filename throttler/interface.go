package throttler

import "context"

// IThrottler is a back pressure pattern implementation.
// Added in place of implementation for convenience of use in external packages.
type IThrottler interface {
	Execute(
		ctx context.Context,
		operation func(ctx context.Context) error,
	) error
}
