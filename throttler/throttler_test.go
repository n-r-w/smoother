//nolint:revive // tests
package throttler

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

func TestNewThrottler(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		th, err := New(10)
		require.NoError(t, err)
		assert.NotNil(t, th)
	})

	t.Run("invalid concurrency", func(t *testing.T) {
		th, err := New(0)
		assert.Error(t, err)
		assert.Nil(t, th)

		th, err = New(-5)
		assert.Error(t, err)
		assert.Nil(t, th)
	})

	t.Run("with timeout option", func(t *testing.T) {
		th, err := New(5, WithTimeout(2*time.Second))
		require.NoError(t, err)
		assert.NotNil(t, th)
	})

	t.Run("with negative timeout", func(t *testing.T) {
		th, err := New(5, WithTimeout(-1*time.Second))
		assert.Error(t, err)
		assert.Nil(t, th)
	})
}

func TestThrottlerExecute(t *testing.T) {
	t.Run("successful execution", func(t *testing.T) {
		th, err := New(1)
		require.NoError(t, err)

		executed := false
		err = th.Execute(context.Background(), func(ctx context.Context) error {
			executed = true
			return nil
		})

		assert.NoError(t, err)
		assert.True(t, executed)
	})

	t.Run("operation error", func(t *testing.T) {
		th, err := New(1)
		require.NoError(t, err)

		expectedErr := errors.New("operation failed")
		err = th.Execute(context.Background(), func(ctx context.Context) error {
			return expectedErr
		})

		assert.Error(t, err)
		assert.Equal(t, expectedErr, err)
	})
}

func TestThrottlerConcurrency(t *testing.T) {
	const maxConcurrency = 3

	th, err := New(maxConcurrency)
	require.NoError(t, err)

	var (
		mu              sync.Mutex
		currentCount    int
		maxActiveCount  int
		operationsDone  int
		operationsTotal = 50
		wg              sync.WaitGroup
	)

	wg.Add(operationsTotal)

	for range operationsTotal {
		go func() {
			defer wg.Done()

			err := th.Execute(context.Background(), func(ctx context.Context) error {
				// Track concurrent executions
				mu.Lock()
				currentCount++
				if currentCount > maxActiveCount {
					maxActiveCount = currentCount
				}
				mu.Unlock()

				// Simulate some work
				time.Sleep(10 * time.Millisecond)

				mu.Lock()
				currentCount--
				operationsDone++
				mu.Unlock()

				return nil
			})

			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	assert.Equal(t, operationsTotal, operationsDone, "All operations should complete")
	assert.LessOrEqual(t, maxActiveCount, maxConcurrency, "Max concurrency should not be exceeded")
}

func TestThrottlerTimeout(t *testing.T) {
	timeout := 50 * time.Millisecond
	th, err := New(1, WithTimeout(timeout))
	require.NoError(t, err)

	t.Run("operation completes before timeout", func(t *testing.T) {
		start := time.Now()
		err = th.Execute(context.Background(), func(ctx context.Context) error {
			time.Sleep(timeout / 2)
			return nil
		})
		elapsed := time.Since(start)

		assert.NoError(t, err)
		assert.Less(t, elapsed, timeout)
	})

	t.Run("operation times out", func(t *testing.T) {
		start := time.Now()
		err = th.Execute(context.Background(), func(ctx context.Context) error {
			// Try to sleep for longer than timeout, but respect context
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(timeout * 3):
				return nil
			}
		})
		elapsed := time.Since(start)

		assert.Error(t, err)
		// Context deadline exceeded or context canceled
		assert.True(t, errors.Is(err, context.DeadlineExceeded))
		assert.GreaterOrEqual(t, elapsed, timeout)
	})
}

func TestThrottlerContextCancellation(t *testing.T) {
	th, err := New(5)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var executeError error

	go func() {
		executeError = th.Execute(ctx, func(ctx context.Context) error {
			// Wait for context cancellation
			<-ctx.Done()
			return ctx.Err()
		})
		close(done)
	}()

	// Wait a bit before cancelling
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		assert.Error(t, executeError)
		assert.True(t, errors.Is(executeError, context.Canceled))
	case <-time.After(time.Second):
		t.Fatal("Operation did not respect context cancellation")
	}
}

func TestThrottlerBlockingBehavior(t *testing.T) {
	// Create a throttler with concurrency limit of 1
	th, err := New(1)
	require.NoError(t, err)

	// Use for synchronizing the test
	var (
		firstStarted  = make(chan struct{})
		blockerWaiter sync.WaitGroup
		blockerDone   = make(chan struct{})
		secondDone    = make(chan struct{})
		wg            sync.WaitGroup
	)

	// Launch a goroutine that will block the throttler
	wg.Add(1)
	blockerWaiter.Add(1)
	go func() {
		defer wg.Done()
		err := th.Execute(context.Background(), func(ctx context.Context) error {
			close(firstStarted)
			blockerWaiter.Wait() // Block until signaled to release
			return nil
		})
		assert.NoError(t, err)
		close(blockerDone)
	}()

	// Wait for the first operation to start
	<-firstStarted

	// Launch a second goroutine that should be blocked
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := th.Execute(context.Background(), func(ctx context.Context) error {
			return nil
		})
		assert.NoError(t, err)
		close(secondDone)
	}()

	// Give some time to verify the second operation is blocked
	time.Sleep(100 * time.Millisecond)
	select {
	case <-secondDone:
		t.Fatal("Second operation completed while throttler should be blocked")
	default:
		// Expected: second operation is blocked
	}

	// Allow the first operation to complete
	blockerWaiter.Done()

	// Verify the operations complete in correct order
	select {
	case <-blockerDone:
		// OK: First operation should complete first
	case <-time.After(time.Second):
		t.Fatal("First operation did not complete in time")
	}

	select {
	case <-secondDone:
		// OK: Second operation should complete after first
	case <-time.After(time.Second):
		t.Fatal("Second operation did not complete in time")
	}

	wg.Wait()
}

func TestThrottlerConcurrentRequestHandling(t *testing.T) {
	// Test how the throttler handles a large number of concurrent requests
	const (
		maxConcurrency  = 10
		operationsTotal = 200
	)

	th, err := New(maxConcurrency)
	require.NoError(t, err)

	var (
		completedOps atomic.Int32
		wg           sync.WaitGroup
	)

	wg.Add(operationsTotal)

	// Launch a large number of concurrent operations
	for i := range operationsTotal {
		go func(id int) {
			defer wg.Done()

			err := th.Execute(context.Background(), func(ctx context.Context) error {
				// Simulate variable work time
				time.Sleep(time.Duration(5+id%10) * time.Millisecond)
				completedOps.Add(1)
				return nil
			})

			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int32(operationsTotal), completedOps.Load())
}
