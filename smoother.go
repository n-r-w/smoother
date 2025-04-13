package smoother

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultTimeout is the default timeout for requests to rate limiter.
	DefaultTimeout = time.Second

	// DefaultQueueSize is the default size of the request queue.
	DefaultQueueSize = 100
)

// Option is a function that configures a RateSmoother.
type Option func(*RateSmoother)

// WithTimeout sets the timeout for requests to rate limiter.
func WithTimeout(timeout time.Duration) Option {
	return func(r *RateSmoother) {
		r.timeout = timeout
	}
}

// WithQueueSize sets the size of the request queue.
func WithQueueSize(size int) Option {
	return func(r *RateSmoother) {
		r.queueSize = size
	}
}

// RateSmoother implements a rate limiter that blocks to ensure that the time spent between multiple.
type RateSmoother struct {
	tryer   ITryer
	timeout time.Duration

	queueSize    int
	requestQueue chan *waitingRequest
	wg           sync.WaitGroup
	quit         chan struct{}
	running      atomic.Bool
}

// NewRateSmoother creates a new RateSmoother instance.
func NewRateSmoother(tryer ITryer, opts ...Option) (*RateSmoother, error) {
	if tryer == nil {
		return nil, fmt.Errorf("NewRateSmoother: nil tryer")
	}

	s := &RateSmoother{
		tryer:   tryer,
		timeout: DefaultTimeout,

		queueSize: DefaultQueueSize,
		quit:      make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	if err := s.validateOptions(); err != nil {
		return nil, fmt.Errorf("NewRateSmoother: %w", err)
	}

	s.requestQueue = make(chan *waitingRequest, s.queueSize)

	return s, nil
}

// Start starts the background processor.
func (r *RateSmoother) Start() {
	if !r.running.CompareAndSwap(false, true) {
		return
	}

	r.wg.Add(1)
	go r.processQueue()
}

// Stop stops the background processor and cleans up resources.
func (r *RateSmoother) Stop() {
	if !r.running.CompareAndSwap(true, false) {
		return
	}

	close(r.quit)
	r.wg.Wait()
}

// waitingRequest represents a request waiting to be processed.
type waitingRequest struct {
	ctx         context.Context
	count       int
	arrivalTime time.Time
	resultCh    chan<- takeResult
	abandoned   atomic.Bool // Indicates if the request was abandoned
}

// takeResult represents the result of a Take operation.
type takeResult struct {
	waitDuration time.Duration
	err          error
}

func (r *RateSmoother) validateOptions() error {
	if r.timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	if r.queueSize <= 0 {
		return errors.New("queue size must be positive")
	}

	return nil
}

// processQueue handles requests in FIFO order
func (r *RateSmoother) processQueue() {
	defer r.wg.Done()

	for {
		select {
		case <-r.quit:
			// Drain any remaining requests with errors when shutting down
			for {
				select {
				case req := <-r.requestQueue:
					if !req.abandoned.Load() { // Don't send to abandoned requests
						req.resultCh <- takeResult{
							waitDuration: time.Since(req.arrivalTime),
							err:          fmt.Errorf("rate smoother shutting down"),
						}
					}
				default:
					// After draining, close the request queue channel
					close(r.requestQueue)
					return
				}
			}
		case req := <-r.requestQueue:
			// Process the request
			r.handleRequest(req)
		}
	}
}

// handleRequest processes a single request
func (r *RateSmoother) handleRequest(req *waitingRequest) {
	if req.ctx.Err() != nil {
		// Handle already canceled context
		return
	}

	var (
		allowed  bool
		waitTime time.Duration
		err      error
	)

	for {
		// Check if the request context is still valid
		select {
		case <-req.ctx.Done():
			if !req.abandoned.Load() {
				req.resultCh <- takeResult{
					waitDuration: time.Since(req.arrivalTime),
					err:          req.ctx.Err(),
				}
			}
			return
		default:
		}

		// Create a context with timeout for the TryTake call
		ctxRequest, cancel := context.WithTimeout(req.ctx, r.timeout)
		allowed, waitTime, err = r.tryer.TryTake(ctxRequest, req.count)
		cancel()

		if err != nil {
			if !req.abandoned.Load() {
				req.resultCh <- takeResult{
					waitDuration: time.Since(req.arrivalTime),
					err:          fmt.Errorf("tryer: %w", err),
				}
			}
			return
		}

		if allowed {
			// Success, send the result
			if !req.abandoned.Load() {
				req.resultCh <- takeResult{
					waitDuration: time.Since(req.arrivalTime),
					err:          nil,
				}
			}
			return
		}

		// Need to wait before trying again
		timer := time.NewTimer(waitTime)
		select {
		case <-req.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if !req.abandoned.Load() {
				req.resultCh <- takeResult{
					waitDuration: time.Since(req.arrivalTime),
					err:          req.ctx.Err(),
				}
			}
			return
		case <-timer.C:
			// Timer expired, try again
			continue
		}
	}
}

// Take blocks to ensure that the time spent between multiple Take calls is on average per/rate.
// The count parameter specifies how many tokens to take at once.
// It returns the time at which function waits for allowance.
func (r *RateSmoother) Take(ctx context.Context, count int) (time.Duration, error) {
	if count <= 0 {
		return 0, fmt.Errorf("invalid count %d", count)
	}

	if !r.running.Load() {
		return 0, errors.New("smoother not running")
	}

	resultCh := make(chan takeResult, 1)

	// Create and enqueue the request
	req := &waitingRequest{
		ctx:         ctx,
		count:       count,
		arrivalTime: time.Now(),
		resultCh:    resultCh,
	}

	// Enqueue the request
	select {
	case r.requestQueue <- req:
		// Request enqueued successfully
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	// Wait for the result
	select {
	case result := <-resultCh:
		return result.waitDuration, result.err
	case <-ctx.Done():
		// Context expired while waiting for result
		req.abandoned.Store(true) // Mark as abandoned
		return time.Since(req.arrivalTime), ctx.Err()
	}
}

// BurstFromRPSFunc is the function to calculate the burst from rps.
type BurstFromRPSFunc func(rps float64) float64

// DefaultBurstFromRPS calculates the empirical dependency of the burst,
// so that it does not freeze rps.
func DefaultBurstFromRPS(rps float64) float64 {
	burst := rps / 500 //nolint:mnd // ok
	if burst < 1 {
		burst = 1
	}
	if burst > 100 { //nolint:mnd // ok
		burst = 100
	}
	return burst
}
