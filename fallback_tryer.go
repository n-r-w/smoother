package smoother

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/eapache/go-resiliency/breaker"
)

const (
	// DefaultCircuitBreakerErrorThreshold error threshold (for opening the breaker)
	DefaultCircuitBreakerErrorThreshold = 1
	// DefaultCircuitBreakerSuccessThreshold success threshold (for closing the breaker)
	DefaultCircuitBreakerSuccessThreshold = 3
	// DefaultCircuitBreakerTimeout timeout (how long to keep the breaker open)
	DefaultCircuitBreakerTimeout = time.Second
)

// FallbackTryerStatus is the status of the FallbackTryer.
type FallbackTryerStatus int

// String returns the string representation of the FallbackTryerStatus.
func (s FallbackTryerStatus) String() string {
	switch s {
	case FallbackTryerStatusMain:
		return "main"
	case FallbackTryerStatusFallback:
		return "fallback"
	default:
		return "unknown"
	}
}

const (
	// FallbackTryerStatusMain main tryer.
	FallbackTryerStatusMain FallbackTryerStatus = 0
	// FallbackTryerStatusFallback fallback tryer.
	FallbackTryerStatusFallback FallbackTryerStatus = 1
)

// StatusChangedFunc is a function that is called when the status of the breaker changes.
type StatusChangedFunc func(ctx context.Context, status FallbackTryerStatus)

// FallbackTryerOption is a function that configures a FallbackTryer.
type FallbackTryerOption func(*FallbackTryer)

// WithFallbackTryerStatusChangedFunc sets the status changed function for the FallbackTryer.
func WithFallbackTryerStatusChangedFunc(statusChangedFunc StatusChangedFunc) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.statusChangedFunc = statusChangedFunc
	}
}

// WithFallbackTryerErrorThreshold sets the error threshold for the FallbackTryer.
func WithFallbackTryerErrorThreshold(errorThreshold int) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.errorThreshold = errorThreshold
	}
}

// WithFallbackTryerSuccessThreshold sets the success threshold for the FallbackTryer.
func WithFallbackTryerSuccessThreshold(successThreshold int) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.successThreshold = successThreshold
	}
}

// WithFallbackTryerTimeout sets the timeout for the FallbackTryer.
func WithFallbackTryerTimeout(timeout time.Duration) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.timeout = timeout
	}
}

// FallbackTryer implements a rate limiter that uses one main Tryer and one fallback Tryer.
// If the main Tryer fails, it uses the fallback Tryer using circuit breaker pattern.
type FallbackTryer struct {
	breaker          *breaker.Breaker
	lastBreakerState atomic.Uint32

	main              Tryer
	fallback          Tryer
	errorThreshold    int
	successThreshold  int
	timeout           time.Duration
	statusChangedFunc StatusChangedFunc
}

var _ Tryer = (*FallbackTryer)(nil)

// NewFallbackTryer creates a new FallbackTryer instance.
func NewFallbackTryer(main, fallback Tryer, opts ...FallbackTryerOption) *FallbackTryer {
	f := &FallbackTryer{
		main:             main,
		fallback:         fallback,
		errorThreshold:   DefaultCircuitBreakerErrorThreshold,
		successThreshold: DefaultCircuitBreakerSuccessThreshold,
		timeout:          DefaultCircuitBreakerTimeout,
	}

	for _, opt := range opts {
		opt(f)
	}

	f.breaker = breaker.New(f.errorThreshold, f.successThreshold, f.timeout)
	f.lastBreakerState.Store(uint32(breaker.Closed))

	return f
}

// TryTake attempts to take n requests.
func (f *FallbackTryer) TryTake(ctx context.Context, count uint32) (bool, time.Duration, error) {
	var (
		waitTime time.Duration
		allowed  bool
	)
	err := f.breaker.Run(func() error {
		var err error
		allowed, waitTime, err = f.main.TryTake(ctx, count)

		// we maintain the load state in fallback, so it was correct
		// when switching. errors are ignored
		_, _, _ = f.fallback.TryTake(ctx, count)
		return err
	})

	switch err {
	case nil:
		// success
		f.openToCloseProcess(ctx)
		return allowed, waitTime, nil
	case breaker.ErrBreakerOpen: // errors.Is is not needed
		// our function wasn't run because the breaker was open
		f.closeToOpenProcess(ctx)
		return f.fallback.TryTake(ctx, count)
	default:
		// some other error
		return allowed, waitTime, nil
	}
}

func (f *FallbackTryer) closeToOpenProcess(ctx context.Context) {
	if f.lastBreakerState.CompareAndSwap(uint32(breaker.Closed), uint32(breaker.Open)) {
		if f.statusChangedFunc != nil {
			f.statusChangedFunc(ctx, FallbackTryerStatusFallback)
		}
	}
}

func (f *FallbackTryer) openToCloseProcess(ctx context.Context) {
	if f.lastBreakerState.CompareAndSwap(uint32(breaker.Open), uint32(breaker.Closed)) {
		if f.statusChangedFunc != nil {
			f.statusChangedFunc(ctx, FallbackTryerStatusMain)
		}
	}
}
