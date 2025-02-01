# Smoother

Smoother is a Go package that provides traffic smoothing capabilities by introducing controlled delays to maintain a target request rate. Unlike traditional rate limiters that reject excess requests, Smoother keeps all requests active while ensuring a consistent throughput through intelligent request spacing.

[![Go Reference](https://pkg.go.dev/badge/github.com/n-r-w/smoother.svg)](https://pkg.go.dev/github.com/n-r-w/smoother)
![CI Status](https://github.com/n-r-w/smoother/actions/workflows/go.yml/badge.svg)

## Features

- 🎯 Maintains target RPS through controlled delays rather than request rejection
- ⏱️ Dynamically adjusts wait times to smooth traffic spikes
- 🔄 Keeps all requests active and processing
- 🌊 Smooths out traffic spikes while ensuring all requests complete
- 📡 Supports both local and distributed (Redis-based) implementations
- 🛠️ Pluggable interface for custom implementations
- 🔌 Thread-safe operation

## Key Benefits

- No dropped connections or rejected requests
- Consistent, predictable throughput
- Better resource utilization through traffic smoothing
- Ideal for scenarios where request completion is more important than immediate error response
- Suitable for both single-instance and distributed systems

## Special features of the implementation [Primary + fallback + circuit breaker](./fallback_tryer.go)

This type of implementation is designed for read-only traffic, since it switches between primary and fallback Tryer simultaneously sending requests to both.
In close state, it selects primary if it's successful and fallback in case of failure.
This means that in close state traffic will be handled by both implementations simultaneously.
The removal of traffic from primary is only done when the circuit breaker is opened.

## Installation

```bash
go get github.com/n-r-w/smoother
```

## Rate Limiter backends

- [Redis Rate](https://github.com/go-redis/redis_rate)
- [Throttled with Redis backend](https://github.com/throttled/throttled)
- [In-memory](./local_tryer.go)
- [Primary + fallback + circuit breaker](./fallback_tryer.go). For example, to use Redis Rate as the primary Tryer and In-memory as the fallback.

## Usage

See the [example](./example/main.go) for usage examples.
