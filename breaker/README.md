# Breaker

Circuit breaker that ensures stable response times by automatically switching between primary and fallback operations. Unlike traditional circuit breakers, this implementation focuses on maintaining consistent performance and minimizing error returns by switching to a fallback operation when the primary becomes unavailable. Has only two states: open and closed. In the open state, it transitions to the backup operation and periodically checks the primary for availability. When the primary is restored, it transitions to the closed state.
