//nolint:revive // tests
package breaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreakerNew(t *testing.T) {
	t.Parallel()

	primary := func(ctx context.Context) error { return nil }

	t.Run("successful initialization", func(t *testing.T) {
		cb, err := New(primary)
		require.NoError(t, err)
		assert.NotNil(t, cb)
		assert.Equal(t, StateClosed, cb.GetState())
	})

	t.Run("missing primary operation", func(t *testing.T) {
		_, err := New(nil)
		require.ErrorIs(t, err, ErrNilOperations)
	})

	t.Run("invalid error threshold", func(t *testing.T) {
		_, err := New(
			primary,
			WithErrorThreshold(-1),
			WithHealthCheckInterval(time.Second),
			WithHealthCheckMaxDuration(2*time.Second),
		)
		require.ErrorIs(t, err, ErrInvalidThresholds)
	})

	t.Run("invalid jitter", func(t *testing.T) {
		_, err := New(
			primary,
			WithJitterMaxPerc(-1),
		)
		require.ErrorIs(t, err, ErrInvalidJitter)
	})

	t.Run("invalid health check interval", func(t *testing.T) {
		_, err := New(
			primary,
			WithHealthCheckInterval(2*time.Second),
			WithHealthCheckMaxDuration(3*time.Second),
			WithJitterMaxPerc(0.1),
		)
		require.ErrorIs(t, err, ErrInvalidHealthCheck)
	})
}

func TestBreakerStartStop(t *testing.T) {
	t.Parallel()

	primary := func(ctx context.Context) error { return nil }
	fallback := func(ctx context.Context) error { return nil }

	cb, err := New(primary)
	require.NoError(t, err)
	require.NotNil(t, cb)

	require.NoError(t, cb.Start(context.Background()))
	require.ErrorIs(t, cb.Start(context.Background()), ErrAlreadyStarted)

	require.NoError(t, cb.Stop())
	require.ErrorIs(t, cb.Stop(), ErrAlreadyStopped)

	opType, err := cb.Run(context.Background(), primary, fallback)
	require.Equal(t, OperationNone, opType)
	require.ErrorIs(t, err, ErrBreakerStopped)
}

func TestBreakerRun(t *testing.T) {
	t.Parallel()

	var (
		ctx            = context.Background()
		primaryFailed  atomic.Bool
		fallbackFailed atomic.Bool
	)

	const (
		errorThreshold         = 3
		successThreshold       = 3
		healthCheckInterval    = 100 * time.Millisecond
		healthCheckMaxDuration = 50 * time.Millisecond
	)

	// Simulate an operation that starts failing after a certain number of calls
	primary := func(_ context.Context) error {
		if primaryFailed.Load() {
			return errors.New("primary operation failed")
		}
		return nil
	}

	fallback := func(_ context.Context) error {
		if fallbackFailed.Load() {
			return errors.New("fallback operation failed")
		}
		return nil
	}

	var (
		stateChangeClosedCount atomic.Int32
		stateChangeOpenCount   atomic.Int32
	)
	stateChangeFunc := func(ctx context.Context, state State) {
		switch state {
		case StateClosed:
			stateChangeClosedCount.Add(1)
		case StateOpen:
			stateChangeOpenCount.Add(1)
		}
	}

	// Create breaker with test configuration
	cb, err := New(
		primary,
		WithErrorThreshold(errorThreshold),
		WithSuccessThreshold(successThreshold),
		WithHealthCheckInterval(healthCheckInterval),
		WithHealthCheckMaxDuration(healthCheckMaxDuration),
		WithJitterMaxPerc(0),
		WithStateChangeFunc(stateChangeFunc),
	)
	require.NoError(t, err)
	require.NotNil(t, cb)

	// Start the circuit breaker
	err = cb.Start(ctx)
	require.NoError(t, err)

	// Simulate fallback failure. This should not affect the primary and the circuit breaker should remain closed.
	primaryFailed.Store(false)
	fallbackFailed.Store(true)
	opType, err := cb.Run(ctx, primary, fallback)
	require.NoError(t, err)
	require.Equal(t, OperationPrimary, opType)
	require.Equal(t, StateClosed, cb.GetState())

	// Simulate primary failure.
	// This should change the state to open, but fallback should still work and return success.
	primaryFailed.Store(true)
	fallbackFailed.Store(false)
	// Wait for state transition to open
	for range errorThreshold - 1 {
		opType, err = cb.Run(ctx, primary, fallback)
		require.NoError(t, err)
		require.Equal(t, OperationFallback, opType)
		require.Equal(t, StateClosed, cb.GetState())
	}
	// Should be open after error threshold
	opType, err = cb.Run(ctx, primary, fallback)
	require.NoError(t, err)
	require.Equal(t, OperationFallback, opType)
	require.Equal(t, StateOpen, cb.GetState())
	require.EqualValues(t, 1, stateChangeOpenCount.Load())
	require.EqualValues(t, 0, stateChangeClosedCount.Load())
	// Fallback should work now
	opType, err = cb.Run(ctx, primary, fallback)
	require.NoError(t, err)
	require.Equal(t, OperationFallback, opType)

	// Success and then failure during health check
	primaryFailed.Store(false) // return to health
	time.Sleep(healthCheckInterval / 2)
	require.Equal(t, StateOpen, cb.GetState()) // not enough time for success threshold
	require.EqualValues(t, 1, stateChangeOpenCount.Load())
	require.EqualValues(t, 0, stateChangeClosedCount.Load())

	primaryFailed.Store(true)                  // primary fails again
	time.Sleep(healthCheckInterval)            // success count should reset
	primaryFailed.Store(false)                 // primary works again
	require.Equal(t, StateOpen, cb.GetState()) // still open
	require.EqualValues(t, 1, stateChangeOpenCount.Load())
	require.EqualValues(t, 0, stateChangeClosedCount.Load())

	time.Sleep(healthCheckInterval / 2)
	require.Equal(t, StateOpen, cb.GetState()) // still open, not enough time for success threshold
	require.EqualValues(t, 1, stateChangeOpenCount.Load())
	require.EqualValues(t, 0, stateChangeClosedCount.Load())

	time.Sleep(healthCheckInterval * (successThreshold + 2))
	require.Equal(t, StateClosed, cb.GetState()) // back to closed
	require.EqualValues(t, 1, stateChangeOpenCount.Load())
	require.EqualValues(t, 1, stateChangeClosedCount.Load())

	// Both primary and fallback fail. Error should be returned immediately.
	primaryFailed.Store(true)
	fallbackFailed.Store(true)
	opType, err = cb.Run(ctx, primary, fallback)
	require.Error(t, err)
	require.Equal(t, OperationNone, opType)
}

func TestBreakerContextCancellation(t *testing.T) {
	t.Parallel()

	var (
		ctx             = context.Background()
		primaryDelayMS  atomic.Int64
		fallbackDelayMS atomic.Int64
		fallbackError   atomic.Bool
	)

	primary := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(primaryDelayMS.Load()) * time.Millisecond):
			return nil
		}
	}
	fallback := func(ctx context.Context) error {
		if fallbackError.Load() {
			return errors.New("fallback error")
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(fallbackDelayMS.Load()) * time.Millisecond):
			return nil
		}
	}

	cb, err := New(primary, WithHealthCheckInterval(time.Millisecond))
	require.NoError(t, err)
	require.NotNil(t, cb)
	require.NoError(t, cb.Start(ctx))

	// Enough time for primary operation
	primaryDelayMS.Store(100)
	fallbackError.Store(true)
	opType, err := cb.Run(ctx, primary, fallback, WithRunPrimaryTimeout(time.Millisecond*400))
	require.Equal(t, OperationPrimary, opType)
	require.NoError(t, err)

	// Not enough time for primary operation and fallback error
	primaryDelayMS.Store(400)
	fallbackError.Store(true)
	opType, err = cb.Run(ctx, primary, fallback, WithRunPrimaryTimeout(time.Millisecond*100))
	require.Equal(t, OperationNone, opType)
	require.Error(t, err)

	// Not enough time for primary and fallback operations
	fallbackError.Store(false)
	primaryDelayMS.Store(200)
	fallbackDelayMS.Store(200)
	opType, err = cb.Run(ctx, primary, fallback,
		WithRunPrimaryTimeout(time.Millisecond*100),
		WithRunFallbackTimeout(time.Millisecond*100),
	)
	require.Equal(t, OperationNone, opType)
	require.Error(t, err)

	// Not enough time for primary, but enough for fallback
	primaryDelayMS.Store(200)
	fallbackDelayMS.Store(10)
	opType, err = cb.Run(ctx, primary, fallback,
		WithRunPrimaryTimeout(time.Millisecond*100),
		WithRunFallbackTimeout(time.Millisecond*100),
	)
	require.Equal(t, OperationFallback, opType)
	require.NoError(t, err)
}

func TestBreakerRaceCondition(t *testing.T) {
	t.Parallel()

	var (
		ctx            = context.Background()
		primaryFailed  atomic.Bool
		fallbackFailed atomic.Bool
	)

	primary := func(_ context.Context) error {
		if primaryFailed.Load() {
			return errors.New("primary operation failed")
		}
		return nil
	}

	fallback := func(_ context.Context) error {
		if fallbackFailed.Load() {
			return errors.New("fallback operation failed")
		}
		return nil
	}

	cb, err := New(
		primary,
		WithErrorThreshold(3),
		WithSuccessThreshold(3),
		WithHealthCheckInterval(300*time.Millisecond),
		WithHealthCheckMaxDuration(200*time.Millisecond),
	)
	require.NoError(t, err)
	require.NoError(t, cb.Start(ctx))

	const (
		concurrentCalls = 100
		maxTime         = time.Second * 5
	)
	wg := sync.WaitGroup{}
	wg.Add(concurrentCalls + 1)
	start := time.Now()

	for i := 0; i < concurrentCalls; i++ {
		go func() {
			defer wg.Done()
			for time.Since(start) < maxTime {
				_, _ = cb.Run(ctx, primary, fallback)
			}
		}()
	}

	// simulate failures
	go func() {
		defer wg.Done()
		var i int64
		for time.Since(start) < maxTime {
			i++
			primaryFailed.Store(i%2 == 0)
			fallbackFailed.Store(i%3 == 0)
		}
	}()

	wg.Wait()
}
