package breaker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultErrorThreshold number of errors before opening the breaker.
	DefaultErrorThreshold = 3
	// DefaultSuccessThreshold number of successes before closing the breaker.
	DefaultSuccessThreshold = 3
	// DefaultHealthCheckInterval interval between health checks.
	DefaultHealthCheckInterval = 5 * time.Second
	// DefaultJitterMaxPerc maximum jitter percentage (0-1) for the health check interval.
	DefaultJitterMaxPerc = 0.1
)

// OperationFunc represents a function that can be executed by the circuit breaker.
type OperationFunc func(ctx context.Context) error

// StateChangeFunc is a function that is called when the state of the circuit breaker changes.
type StateChangeFunc func(ctx context.Context, state State)

// State represents the current state of the circuit breaker.
type State int32

// String returns a string representation of the State.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

const (
	// StateClosed indicates that the primary operation is being used.
	StateClosed State = iota
	// StateOpen indicates that the fallback operation is being used.
	StateOpen
)

// OperationType represents the type of executed operation.
type OperationType int

const (
	// OperationNone indicates that no operation was executed.
	OperationNone OperationType = iota
	// OperationPrimary indicates that the primary operation was executed.
	OperationPrimary
	// OperationFallback indicates that the fallback operation was executed.
	OperationFallback
)

// Breaker implements a circuit breaker pattern that switches between
// primary and fallback operations based on failure rates and health checks.
type Breaker struct {
	stateChangeFunc StateChangeFunc

	errorThreshold   int
	successThreshold int

	healthCheckFunc        OperationFunc
	healthCheckInterval    time.Duration
	healthCheckMaxDuration time.Duration

	jitterMaxPerc float32

	stopped      atomic.Bool
	state        atomic.Int32
	failureCount atomic.Uint32
	successCount atomic.Uint32

	healthCheckStop chan struct{}
	wg              sync.WaitGroup
}

// New creates a new Breaker instance.
func New(healthCheckFunc OperationFunc, opts ...Option) (*Breaker, error) {
	if healthCheckFunc == nil {
		return nil, ErrNilOperations
	}

	cb := &Breaker{
		healthCheckFunc:     healthCheckFunc,
		errorThreshold:      DefaultErrorThreshold,
		successThreshold:    DefaultSuccessThreshold,
		healthCheckInterval: DefaultHealthCheckInterval,
		jitterMaxPerc:       DefaultJitterMaxPerc,
		healthCheckStop:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(cb)
	}

	if cb.errorThreshold <= 0 || cb.successThreshold <= 0 {
		return nil, ErrInvalidThresholds
	}

	if cb.jitterMaxPerc < 0 || cb.jitterMaxPerc > 1 {
		return nil, ErrInvalidJitter
	}

	if cb.healthCheckMaxDuration >= cb.healthCheckInterval {
		return nil, ErrInvalidHealthCheck
	}

	cb.state.Store(int32(StateClosed))
	cb.stopped.Store(true)

	return cb, nil
}

// Start starts the circuit breaker.
func (cb *Breaker) Start(ctx context.Context) error {
	if !cb.stopped.CompareAndSwap(true, false) {
		return ErrAlreadyStarted
	}

	cb.startHealthCheck(context.WithoutCancel(ctx))

	return nil
}

// Stop stops the circuit breaker and its background health checks.
func (cb *Breaker) Stop() error {
	if cb.stopped.CompareAndSwap(false, true) {
		close(cb.healthCheckStop)
		cb.wg.Wait()
		return nil
	}

	return ErrAlreadyStopped
}

// Run executes either the primary or fallback operation based on the current state.
func (cb *Breaker) Run(ctx context.Context, primary, fallback OperationFunc, opts ...RunOption) (OperationType, error) {
	if cb.stopped.Load() {
		return OperationNone, ErrBreakerStopped
	}

	if primary == nil || fallback == nil {
		return OperationNone, ErrNilOperations
	}

	var runOpts runOptions
	for _, opt := range opts {
		opt(&runOpts)
	}

	// Validate timeout values
	if runOpts.primaryTimeout < 0 || runOpts.fallbackTimeout < 0 {
		return OperationNone, ErrInvalidRunTimeout
	}

	// we need to cancel the fallback context if the primary operation succeeds
	fallbackCtx, fallbackCancel := context.WithCancel(ctx)
	defer fallbackCancel()

	if runOpts.fallbackTimeout > 0 {
		// override the context timeout for the fallback operation using custom timeout
		fallbackCtx, fallbackCancel = context.WithTimeout(fallbackCtx, runOpts.fallbackTimeout)
		defer fallbackCancel()
	}

	if cb.GetState() == StateClosed {
		primaryCtx := ctx
		if runOpts.primaryTimeout > 0 {
			var primaryCancel context.CancelFunc
			primaryCtx, primaryCancel = context.WithTimeout(ctx, runOpts.primaryTimeout)
			defer primaryCancel()
		}

		var (
			primaryStoppedCh        = make(chan struct{})
			fallbackStoppedCh       = make(chan struct{})
			primaryErr, fallbackErr error
		)

		// start primary
		go func() {
			defer func() {
				if r := recover(); r != nil {
					primaryErr = fmt.Errorf("panic in primary: %v", r)
				}
				close(primaryStoppedCh)
			}()

			primaryErr = primary(primaryCtx)
		}()

		// start fallback
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fallbackErr = fmt.Errorf("panic in fallback: %v", r)
				}
				close(fallbackStoppedCh)
			}()

			fallbackErr = fallback(fallbackCtx)
		}()

		// wait for primary to finish
		<-primaryStoppedCh
		if primaryErr != nil {
			// Increment failure count and potentially transition to open state
			if cb.failureCount.Add(1) >= uint32(cb.errorThreshold) { //nolint:gosec // ok
				cb.transitionToOpen(ctx)
			}

			// if primary failed, use fallback
			<-fallbackStoppedCh
			if fallbackErr != nil {
				return OperationNone, fmt.Errorf("primary and fallback failed: %w", errors.Join(primaryErr, fallbackErr))
			}
			return OperationFallback, nil
		}

		// primary succeeded, so we don't need to wait for fallback
		fallbackCancel()
		<-fallbackStoppedCh

		return OperationPrimary, nil
	}

	// In open state, use fallback
	if err := fallback(fallbackCtx); err != nil {
		return OperationNone, fmt.Errorf("fallback failed: %w", err)
	}
	return OperationFallback, nil
}

// GetState returns the current state of the circuit breaker.
func (cb *Breaker) GetState() State {
	return State(cb.state.Load())
}

func (cb *Breaker) transitionToClosed(ctx context.Context) {
	if cb.state.CompareAndSwap(int32(StateOpen), int32(StateClosed)) {
		cb.successCount.Store(0)
		cb.failureCount.Store(0)
		if cb.stateChangeFunc != nil {
			cb.stateChangeFunc(ctx, StateClosed)
		}
	}
}

func (cb *Breaker) transitionToOpen(ctx context.Context) {
	if cb.state.CompareAndSwap(int32(StateClosed), int32(StateOpen)) {
		cb.successCount.Store(0)
		cb.failureCount.Store(0)
		if cb.stateChangeFunc != nil {
			cb.stateChangeFunc(ctx, StateOpen)
		}
	}
}

func (cb *Breaker) startHealthCheck(ctx context.Context) { //nolint:gocognit // ok
	cb.wg.Add(1)

	go func() {
		defer cb.wg.Done()

		for {
			var jitter time.Duration
			if cb.jitterMaxPerc > 0 {
				jitter = time.Duration(
					rand.Int63n(int64(float64(cb.healthCheckInterval) * float64(cb.jitterMaxPerc)))) //nolint:gosec,mnd // ok
			}
			timer := time.NewTimer(cb.healthCheckInterval + jitter)

			select {
			case <-cb.healthCheckStop:
				return
			case <-timer.C:
				if cb.GetState() == StateClosed {
					continue
				}

				var (
					ctxCall = ctx
					cancel  context.CancelFunc
				)
				if cb.healthCheckMaxDuration > 0 {
					ctxCall, cancel = context.WithTimeout(ctx, cb.healthCheckMaxDuration)
				}

				if err := cb.healthCheckFunc(ctxCall); err == nil {
					if cb.successCount.Add(1) >= uint32(cb.successThreshold) { //nolint:gosec // ok
						cb.transitionToClosed(ctxCall)
						if cancel != nil {
							cancel()
						}
						continue
					}
				} else {
					cb.successCount.Store(0)
					timer.Reset(cb.healthCheckInterval + jitter)
				}

				if cancel != nil {
					cancel()
				}
			}
		}
	}()
}
