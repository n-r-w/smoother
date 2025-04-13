package smoother

import (
	"context"
	"fmt"
	"time"

	"github.com/n-r-w/smoother/breaker"
	"github.com/samber/lo"
)

type (
	// FallbackTryerErrorFunc is a function that is called when an error occurs.
	FallbackTryerErrorFunc func(ctx context.Context, err error)

	// FallbackTryerOption is a function that configures a FallbackTryer.
	FallbackTryerOption func(*FallbackTryer)
)

// WithFallbackTryerErrorFunc sets the error function for the FallbackTryer.
func WithFallbackTryerErrorFunc(errorFunc FallbackTryerErrorFunc) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.errorFunc = errorFunc
	}
}

// WithFallbackTryerBreakerOptions sets the circuit breaker options for the FallbackTryer.
func WithFallbackTryerBreakerOptions(options ...breaker.Option) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.breakerOptions = options
	}
}

// WithFallbackTryerBreakerRunOptions sets the circuit breaker run options for the FallbackTryer.
func WithFallbackTryerBreakerRunOptions(options ...breaker.RunOption) FallbackTryerOption {
	return func(f *FallbackTryer) {
		f.breakerRunOptions = options
	}
}

// FallbackTryer implements a rate limiter that uses one primary Tryer and one fallback Tryer.
// If the primary Tryer fails, it uses the fallback Tryer using circuit breaker pattern.
type FallbackTryer struct {
	breaker *breaker.Breaker

	primary  ITryer
	fallback ITryer

	breakerOptions    []breaker.Option
	breakerRunOptions []breaker.RunOption

	errorFunc FallbackTryerErrorFunc
}

var _ ITryer = (*FallbackTryer)(nil)

// NewFallbackTryer creates a new FallbackTryer instance.
func NewFallbackTryer(primary, fallback ITryer, opts ...FallbackTryerOption) (*FallbackTryer, error) {
	if primary == nil {
		return nil, fmt.Errorf("NewFallbackTryer: nil primary tryer")
	}
	if fallback == nil {
		return nil, fmt.Errorf("NewFallbackTryer: nil fallback tryer")
	}

	f := &FallbackTryer{
		primary:  primary,
		fallback: fallback,
	}

	for _, opt := range opts {
		opt(f)
	}

	if err := f.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewFallbackTryer: %w", err)
	}

	br, err := breaker.New(func(ctx context.Context) error {
		if _, _, err := primary.TryTake(ctx, 1); err != nil {
			return fmt.Errorf("NewFallbackTryer: %w", err)
		}
		return nil
	}, f.breakerOptions...)
	if err != nil {
		return nil, fmt.Errorf("NewFallbackTryer: %w", err)
	}

	f.breaker = br
	return f, nil
}

// Start starts FallbackTryer.
func (f *FallbackTryer) Start(ctx context.Context) error {
	return f.breaker.Start(ctx)
}

// Stop stops FallbackTryer.
func (f *FallbackTryer) Stop() error {
	return f.breaker.Stop()
}

// TryTake attempts to take n requests.
func (f *FallbackTryer) TryTake(ctx context.Context, count int) (bool, time.Duration, error) {
	var (
		start                           = time.Now()
		allowedPrimary, allowedFallback bool
	)

	primaryFunc := func(ctx context.Context) error {
		var err error
		allowedPrimary, _, err = f.primary.TryTake(ctx, count)
		return err
	}

	fallbackFunc := func(ctx context.Context) error {
		var err error
		allowedFallback, _, err = f.fallback.TryTake(ctx, count)
		return err
	}

	opType, err := f.breaker.Run(ctx, primaryFunc, fallbackFunc, f.breakerRunOptions...)
	if err != nil {
		if f.errorFunc != nil {
			f.errorFunc(ctx, err)
		}

		return false, time.Since(start), err
	}

	return lo.Ternary(opType == breaker.OperationPrimary, allowedPrimary, allowedFallback), time.Since(start), nil
}

// GetState returns the current state of the circuit breaker.
func (f *FallbackTryer) GetState() breaker.State {
	return f.breaker.GetState()
}

func (f *FallbackTryer) validateOptions() error {
	return nil
}

// SetRate updates only the RPS (requests per second) value of the FallbackTryer.
func (f *FallbackTryer) SetRate(rps float64) error {
	if err := f.primary.SetRate(rps); err != nil {
		return fmt.Errorf("FallbackTryer.SetRate: %w", err)
	}
	if err := f.fallback.SetRate(rps); err != nil {
		return fmt.Errorf("FallbackTryer.SetRate: %w", err)
	}
	return nil
}

// SetMultiplier updates only the multiplier value of the FallbackTryer.
func (f *FallbackTryer) SetMultiplier(multiplier float64) error {
	if err := f.primary.SetMultiplier(multiplier); err != nil {
		return fmt.Errorf("FallbackTryer.SetMultiplier: %w", err)
	}
	if err := f.fallback.SetMultiplier(multiplier); err != nil {
		return fmt.Errorf("FallbackTryer.SetMultiplier: %w", err)
	}
	return nil
}

// GetRate returns the current rate limit in requests per second.
func (f *FallbackTryer) GetRate() float64 {
	return f.primary.GetRate()
}

// GetMultiplier returns the current multiplier value.
func (f *FallbackTryer) GetMultiplier() float64 {
	return f.primary.GetMultiplier()
}
