//nolint:revive // errors
package breaker

import "errors"

var (
	ErrNilOperations      = errors.New("primary and fallback operations must be provided")
	ErrInvalidThresholds  = errors.New("invalid error threshold or success threshold")
	ErrInvalidJitter      = errors.New("jitter max percentage must be in the range [0, 1]")
	ErrInvalidHealthCheck = errors.New("health check max duration must be lower than health check interval plus max jitter")
	ErrAlreadyStarted     = errors.New("circuit breaker is already started")
	ErrAlreadyStopped     = errors.New("circuit breaker is already stopped")
	ErrBreakerStopped     = errors.New("circuit breaker is stopped")
	ErrInvalidRunTimeout  = errors.New("primary and fallback timeouts must be non-negative")
)
