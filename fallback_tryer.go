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

// FallbackTryer implements a rate limiter that uses one main Tryer and one fallback Tryer.
// If the main Tryer fails, it uses the fallback Tryer using circuit breaker pattern.
type FallbackTryer struct {
	breaker *breaker.Breaker

	main     Tryer
	fallback Tryer

	breakerOptions    []breaker.Option
	breakerRunOptions []breaker.RunOption

	errorFunc FallbackTryerErrorFunc
}

var _ Tryer = (*FallbackTryer)(nil)

// NewFallbackTryer creates a new FallbackTryer instance.
func NewFallbackTryer(main, fallback Tryer, opts ...FallbackTryerOption) (*FallbackTryer, error) {
	if main == nil {
		return nil, fmt.Errorf("NewFallbackTryer: nil main tryer")
	}
	if fallback == nil {
		return nil, fmt.Errorf("NewFallbackTryer: nil fallback tryer")
	}

	f := &FallbackTryer{
		main:     main,
		fallback: fallback,
	}

	for _, opt := range opts {
		opt(f)
	}

	if err := f.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewFallbackTryer: %w", err)
	}

	br, err := breaker.New(func(ctx context.Context) error {
		if _, _, err := main.TryTake(ctx, 1); err != nil {
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
		start                        = time.Now()
		allowedMain, allowedFallback bool
	)

	mainFunc := func(ctx context.Context) error {
		var err error
		allowedMain, _, err = f.main.TryTake(ctx, count)
		return err
	}

	fallbackFunc := func(ctx context.Context) error {
		var err error
		allowedFallback, _, err = f.fallback.TryTake(ctx, count)
		return err
	}

	opType, err := f.breaker.Run(ctx, mainFunc, fallbackFunc, f.breakerRunOptions...)
	if err != nil {
		if f.errorFunc != nil {
			f.errorFunc(ctx, err)
		}

		return false, time.Since(start), err
	}

	return lo.Ternary(opType == breaker.OperationPrimary, allowedMain, allowedFallback), time.Since(start), nil
}

// GetState returns the current state of the circuit breaker.
func (f *FallbackTryer) GetState() breaker.State {
	return f.breaker.GetState()
}

func (f *FallbackTryer) validateOptions() error {
	return nil
}
