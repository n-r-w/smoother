package breaker

import "time"

// Option defines optional parameters for Breaker.
type Option func(*Breaker)

// WithErrorThreshold sets the error threshold for the circuit breaker.
// Default: DefaultErrorThreshold.
func WithErrorThreshold(threshold int) Option {
	return func(cb *Breaker) {
		cb.errorThreshold = threshold
	}
}

// WithSuccessThreshold sets the success threshold for the circuit breaker.
// Default: DefaultSuccessThreshold.
func WithSuccessThreshold(threshold int) Option {
	return func(cb *Breaker) {
		cb.successThreshold = threshold
	}
}

// WithHealthCheckInterval sets interval between health checks.
// Transition to closed state after breakerSuccessThreshold*breakerHealthCheckInterval
// Default: DefaultHealthCheckInterval.
func WithHealthCheckInterval(interval time.Duration) Option {
	return func(cb *Breaker) {
		cb.healthCheckInterval = interval
	}
}

// WithHealthCheckMaxDuration sets the maximum duration for the health check.
// Should be lower than HealthCheckInterval.
// Default: DefaultHealthCheckInterval.
func WithHealthCheckMaxDuration(maxDuration time.Duration) Option {
	return func(cb *Breaker) {
		cb.healthCheckMaxDuration = maxDuration
	}
}

// WithJitterMaxPerc sets the maximum jitter percentage (0-1) for the health check interval.
// Default: DefaultJitterMaxPerc.
func WithJitterMaxPerc(jitterMaxPerc float32) Option {
	return func(cb *Breaker) {
		cb.jitterMaxPerc = jitterMaxPerc
	}
}

// WithStateChangeFunc sets the state change callback function for the circuit breaker.
// Default: not set.
func WithStateChangeFunc(stateChangeFunc StateChangeFunc) Option {
	return func(cb *Breaker) {
		cb.stateChangeFunc = stateChangeFunc
	}
}

// RunOption defines optional parameters for Run.
type RunOption func(*runOptions)

// WithRunPrimaryTimeout sets the timeout for the main operation.
// Default: not set.
func WithRunPrimaryTimeout(timeout time.Duration) RunOption {
	return func(opts *runOptions) {
		opts.primaryTimeout = timeout
	}
}

// WithRunFallbackTimeout sets the timeout for the fallback operation.
// Default: not set.
func WithRunFallbackTimeout(timeout time.Duration) RunOption {
	return func(opts *runOptions) {
		opts.fallbackTimeout = timeout
	}
}

type runOptions struct {
	primaryTimeout  time.Duration
	fallbackTimeout time.Duration
}
