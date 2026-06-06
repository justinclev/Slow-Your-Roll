# ratelimiter

Production-grade rate limiting library for Go. Structured with Domain-Driven Design principles: storage backends are pluggable, algorithms are first-class types, and the entire stack is deterministically testable without mocks or global state.

## Features

- **Four algorithms:** Token Bucket, Fixed Window, Sliding Window Log, Sliding Window Counter
- **Two storage backends:** In-memory (single-process) and Redis (distributed)
- **Atomic in-memory operations:** `domain.Transactional` eliminates TOCTOU races in the memory store
- **HTTP middleware:** net/http adapter with standard rate-limit headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`, `Retry-After`)
- **Prometheus metrics:** opt-in `Collector` wrapper that records allowed/denied counters and token-remaining histograms
- **Fully testable:** `clock.Clock` abstraction + `testclock.Clock` eliminate time-dependent flakiness

---

## Algorithm Guide

| Algorithm | Use Case | Memory per Key |
|---|---|---|
| Token Bucket | Smooth bursts, default choice | O(1) |
| Fixed Window | Simple per-interval counters, lowest overhead | O(1) |
| Sliding Window Log | Precise fairness at window boundaries | O(limit) |
| Sliding Window Counter | Approximation of sliding log, low memory | O(1) |

---

## Installation

```bash
go get github.com/justinclev/slow-your-roll
```

Requires Go 1.22+.

---

## Quick Start

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/justinclev/slow-your-roll/algorithm/tokenbucket"
    "github.com/justinclev/slow-your-roll/domain"
    "github.com/justinclev/slow-your-roll/limiter"
    "github.com/justinclev/slow-your-roll/middleware"
    "github.com/justinclev/slow-your-roll/store/memory"
)

func main() {
    // 1. Define a policy: 100 requests/minute, burst up to 100.
    policy, err := domain.NewPolicy(100, time.Minute, 100)
    if err != nil {
        log.Fatal(err)
    }

    // 2. Create a limiter wired with an algorithm and store.
    store := memory.New()
    defer store.Close()

    l := limiter.New(policy, tokenbucket.New(), store)

    // 3. Attach to an HTTP handler.
    mux := http.NewServeMux()
    mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("ok"))
    })

    ipKeyFn := func(r *http.Request) (domain.Key, error) {
        return domain.NewKey(r.RemoteAddr)
    }

    http.ListenAndServe(":8080", middleware.Handler(l, ipKeyFn)(mux))
}
```

---

## Integrating Algorithms

### Token Bucket (default)

```go
import "github.com/justinclev/slow-your-roll/algorithm/tokenbucket"

l := limiter.New(policy, tokenbucket.New(), store)
```

### Fixed Window

```go
import "github.com/justinclev/slow-your-roll/algorithm/fixedwindow"

l := limiter.New(policy, fixedwindow.New(), store)
```

### Sliding Window Log

```go
import "github.com/justinclev/slow-your-roll/algorithm/slidingwindowlog"

l := limiter.New(policy, slidingwindowlog.New(), store)
```

### Sliding Window Counter

```go
import "github.com/justinclev/slow-your-roll/algorithm/slidingwindowcounter"

l := limiter.New(policy, slidingwindowcounter.New(), store)
```

---

## Storage Backends

### In-Memory (single process)

```go
import "github.com/justinclev/slow-your-roll/store/memory"

store := memory.New()
defer store.Close() // stops background cleanup goroutine
```

The memory store implements `domain.Transactional`, ensuring the full read-modify-write cycle for each `Allow` call is atomic. Expired entries are evicted in the background every 5 minutes.

### Redis (distributed)

```go
import (
    goredis "github.com/redis/go-redis/v9"
    rstore "github.com/justinclev/slow-your-roll/store/redis"
)

client := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
store := rstore.New(client, "rl:") // prefix namespaces keys
```

`redis.UniversalClient` is accepted, so standalone, Sentinel, and Cluster clients all work without changes.

> **Atomicity note:** The Redis store uses separate GET and SET commands. Under concurrent writes from multiple processes, a TOCTOU race exists. For distributed deployments requiring strict accuracy, wrap the algorithm logic in a Lua script instead of using this store directly. This is tracked as a v2 enhancement.

---

## HTTP Middleware

```go
// KeyFunc extracts the rate-limit subject from the request.
keyFn := func(r *http.Request) (domain.Key, error) {
    apiKey := r.Header.Get("X-API-Key")
    return domain.NewKey(apiKey)
}

// Wrap any http.Handler.
rateLimited := middleware.Handler(l, keyFn)(myHandler)

// Custom rejection response.
rateLimited = middleware.Handler(l, keyFn,
    middleware.WithOnDenied(func(w http.ResponseWriter, r *http.Request, result domain.Result) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusTooManyRequests)
        fmt.Fprintf(w, `{"error":"rate_limited","retry_after":%d}`, int(result.RetryAfter.Seconds()))
    }),
)(myHandler)
```

Headers set on every response:

| Header | Description |
|---|---|
| `X-RateLimit-Limit` | Configured limit |
| `X-RateLimit-Remaining` | Remaining capacity |
| `X-RateLimit-Reset` | Unix timestamp of window reset |
| `Retry-After` | Integer seconds until retry (denied responses only) |

On store errors the middleware **fails open** — the request is passed through and no headers are set. Log store errors at the application layer.

---

## Prometheus Metrics

Optional; callers that do not import the `metrics` package pay zero cost.

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/justinclev/slow-your-roll/metrics"
)

reg := prometheus.NewRegistry()

// Wrap any algorithm with the collector.
algo, err := metrics.NewCollector(tokenbucket.New(), reg)
if err != nil {
    log.Fatal(err)
}

l := limiter.New(policy, algo, store)
```

Exposed metrics:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ratelimiter_requests_allowed_total` | Counter | `key_prefix` | Requests passed through |
| `ratelimiter_requests_denied_total` | Counter | `key_prefix` | Requests rejected |
| `ratelimiter_tokens_remaining` | Histogram | `key_prefix` | Remaining capacity at check time |

`key_prefix` is the first colon-segment of the key (`"user:42"` → `"user"`) to prevent unbounded label cardinality.

---

## Composing Namespaced Keys

```go
// Single segment
key, _ := domain.NewKey("192.168.1.1")

// Multi-segment namespace
key = domain.Compose("user", userID)          // "user:abc123"
key = domain.Compose("tenant", tid, "upload") // "tenant:acme:upload"
```

---

## Extending the Library

Implement any of these interfaces to plug in custom behaviour without forking:

```go
// Custom storage backend (DynamoDB, Postgres, etc.)
type domain.Store interface {
    Get(ctx context.Context, key Key) (Entry, bool, error)
    Set(ctx context.Context, key Key, entry Entry) error
    Delete(ctx context.Context, key Key) error
}

// Atomic read-modify-write (optional but recommended)
type domain.Transactional interface {
    domain.Store
    Transact(ctx context.Context, key Key, fn func(Entry, bool) (Entry, error)) error
}

// Custom algorithm
type domain.Algorithm interface {
    Allow(ctx context.Context, key Key, policy Policy, store Store, now time.Time) (Result, error)
}

// Custom time source (useful in tests)
type clock.Clock interface {
    Now() time.Time
}
```

---

## Building

```bash
go build ./...
```

---

## Testing

### Unit tests

```bash
go test -race ./...
```

### With coverage

```bash
go test -race -coverprofile=coverage.out -covermode=atomic ./...
go tool cover -func=coverage.out | grep total
```

Coverage target: **90%** (enforced in CI).

### Integration tests (requires Redis)

```bash
# Default: localhost:6379
go test -tags=integration ./store/redis/...

# Custom address
REDIS_ADDR=redis.example.com:6379 go test -tags=integration ./store/redis/...
```

---

## CI

GitHub Actions runs three jobs on every push and pull request to `main`:

| Job | Command |
|---|---|
| **Test** | `go test -race -coverprofile=coverage.out ./...` + 90% threshold check |
| **Lint** | `golangci-lint run` (errcheck, staticcheck, revive, and more) |
| **Build** | `go build ./...` |

See [`.github/workflows/ci.yml`](.github/workflows/ci.yml) for full configuration.

---

## Architecture

```
ratelimiter/
├── domain/          # Value objects (Policy, Result, Key) + ports (Store, Algorithm, Clock)
├── limiter/         # Aggregate root — wires policy, algorithm, store, clock
├── algorithm/       # Token Bucket, Fixed Window, Sliding Window Log, Sliding Window Counter
├── store/           # memory (single-process), redis (distributed)
├── middleware/       # net/http adapter
├── metrics/         # Prometheus collector (opt-in)
├── clock/           # Clock interface + production implementation
└── internal/        # testclock, testerrors (not part of public API)
```

All algorithms are stateless structs. State lives entirely in the store. Callers wire dependencies at the composition root (main or wire); application code imports only `domain`, `limiter`, and `middleware`.

---

## Policy Mutations

`Policy` is immutable. Use the `With*` methods to derive new policies:

```go
base, _ := domain.NewPolicy(100, time.Minute, 100)

// Derived policies share no mutable state.
perHour, _ := base.WithWindow(time.Hour)
higherBurst, _ := base.WithBurst(200)
lowerLimit, _ := base.WithLimit(50)
```

---

## License

See [LICENSE](LICENSE).
