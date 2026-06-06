# Go Rate Limiting Library — Technical Specification

**Version:** 1.1.0  
**Status:** Draft  
**Target Audience:** Senior Engineers / Architects

---

## 1. Purpose & Goals

This document specifies the design of `ratelimiter` — a Go library for production-grade HTTP and general-purpose rate limiting. It is:

- Structured using Domain-Driven Design (DDD) with clear boundaries between domain, application, and infrastructure layers
- Dependency-inverted so storage backends are interchangeable (in-memory, Redis, etc.)
- Fully testable with no hidden global state
- Idiomatic Go — no reflection, no init(), no magic

---

## 2. Non-Goals

- Not a reverse proxy or middleware framework
- Not opinionated about HTTP routing libraries
- Not responsible for Redis Cluster topology — callers provide a pre-configured client
- `MultiLimiter` (chained per-user + per-tenant policies) is deferred to v2

---

## 3. Algorithms Supported

| Algorithm | Use Case |
|---|---|
| Token Bucket | Smooth burst tolerance; default choice |
| Fixed Window | Simple per-interval counters |
| Sliding Window Log | Precise fairness; higher memory cost |
| Sliding Window Counter | Approximation of sliding log; low memory |

Each algorithm is a first-class domain concept, not a flag on a shared struct.

---

## 4. Domain Model

### 4.1 Ubiquitous Language

| Term | Definition |
|---|---|
| **Limiter** | The aggregate root. Owns a policy and delegates to a store. |
| **Policy** | Immutable value object describing the allowed rate (limit, window, burst). |
| **Key** | Arbitrary string identifying the subject being rate-limited (user ID, IP, tenant). |
| **Result** | Value object returned after an Allow check: allowed/denied, remaining, reset time. |
| **Store** | Port (interface) for persisting counter state. |
| **Algorithm** | Strategy interface. Each implementation encapsulates one counting algorithm. |
| **Clock** | Abstraction over `time.Now()` to allow deterministic testing. |

### 4.2 Invariants

- A `Policy` is immutable once constructed. Any mutation returns a new `Policy`.
- `Limiter.Allow` is the only method that mutates state; all other methods are reads.
- `Result.Remaining` is never negative.
- `Result.RetryAfter` is zero-valued when `Result.Allowed` is `true`.

---

## 5. Package Structure

```
ratelimiter/
├── domain/
│   ├── policy.go           # Policy value object
│   ├── result.go           # Result value object
│   ├── key.go              # Key type alias + validation
│   ├── algorithm.go        # Algorithm port (interface)
│   └── store.go            # Store port (interface)
│
├── limiter/
│   └── limiter.go          # Limiter aggregate root + constructor
│
├── algorithm/
│   ├── tokenbucket/
│   │   ├── tokenbucket.go
│   │   └── tokenbucket_test.go
│   ├── fixedwindow/
│   │   ├── fixedwindow.go
│   │   └── fixedwindow_test.go
│   ├── slidingwindowlog/
│   │   ├── slidingwindowlog.go
│   │   └── slidingwindowlog_test.go
│   └── slidingwindowcounter/
│       ├── slidingwindowcounter.go
│       └── slidingwindowcounter_test.go
│
├── store/
│   ├── memory/
│   │   ├── memory.go
│   │   └── memory_test.go
│   └── redis/
│       ├── redis.go
│       └── redis_test.go
│
├── middleware/
│   ├── http.go             # net/http middleware adapter
│   └── http_test.go
│
├── metrics/
│   ├── prometheus.go       # Prometheus collector (shipped with library)
│   └── prometheus_test.go
│
├── clock/
│   ├── clock.go            # Clock interface
│   └── real.go             # time.Now() implementation
│
├── internal/
│   └── testclock/
│       └── testclock.go    # Controllable clock for tests
│
├── .github/
│   └── workflows/
│       └── ci.yml
│
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

---

## 6. Domain Types

### 6.1 `domain/policy.go`

```go
package domain

import (
    "errors"
    "time"
)

// Policy is an immutable value object describing how many requests
// are allowed within a given window, and how much burst is permitted.
type Policy struct {
    limit  int
    window time.Duration
    burst  int
}

var (
    ErrInvalidLimit  = errors.New("limit must be greater than zero")
    ErrInvalidWindow = errors.New("window must be greater than zero")
    ErrInvalidBurst  = errors.New("burst must be >= limit")
)

// NewPolicy constructs a validated Policy.
// burst is the maximum instantaneous allowance; it must be >= limit.
func NewPolicy(limit int, window time.Duration, burst int) (Policy, error) {
    if limit <= 0 {
        return Policy{}, ErrInvalidLimit
    }
    if window <= 0 {
        return Policy{}, ErrInvalidWindow
    }
    if burst < limit {
        return Policy{}, ErrInvalidBurst
    }
    return Policy{limit: limit, window: window, burst: burst}, nil
}

func (p Policy) Limit() int           { return p.limit }
func (p Policy) Window() time.Duration { return p.window }
func (p Policy) Burst() int           { return p.burst }

// WithLimit returns a new Policy with the given limit.
func (p Policy) WithLimit(limit int) (Policy, error) {
    return NewPolicy(limit, p.window, p.burst)
}
```

### 6.2 `domain/result.go`

```go
package domain

import "time"

// Result is an immutable value object returned by a rate limit check.
type Result struct {
    Allowed    bool
    Remaining  int
    ResetAt    time.Time
    RetryAfter time.Duration // zero when Allowed == true
}
```

### 6.3 `domain/store.go`

```go
package domain

import (
    "context"
    "time"
)

// Entry is the opaque counter state persisted per key.
// Algorithms define their own concrete state; the store serialises it.
type Entry struct {
    Data      []byte    // algorithm-encoded state
    ExpiresAt time.Time
}

// Store is the persistence port. Implementations must be safe for
// concurrent use by multiple goroutines.
type Store interface {
    // Get retrieves an entry. Returns (zero Entry, false, nil) when missing.
    Get(ctx context.Context, key Key) (Entry, bool, error)
    // Set upserts an entry with the given expiry.
    Set(ctx context.Context, key Key, entry Entry) error
    // Delete removes an entry. Idempotent.
    Delete(ctx context.Context, key Key) error
}
```

### 6.4 `domain/algorithm.go`

```go
package domain

import (
    "context"
    "time"
)

// Algorithm is the strategy port. Each counting algorithm implements this.
// Allow must be atomic with respect to the provided Store.
type Algorithm interface {
    Allow(ctx context.Context, key Key, policy Policy, store Store, now time.Time) (Result, error)
}
```

### 6.5 `domain/key.go`

```go
package domain

import (
    "errors"
    "strings"
)

// Key identifies the subject being rate limited.
type Key string

var ErrEmptyKey = errors.New("key must not be empty")

// NewKey constructs a validated Key.
func NewKey(raw string) (Key, error) {
    if strings.TrimSpace(raw) == "" {
        return "", ErrEmptyKey
    }
    return Key(raw), nil
}

// Compose joins segments into a namespaced key (e.g. "user:42:upload").
func Compose(segments ...string) Key {
    return Key(strings.Join(segments, ":"))
}
```

---

## 7. Limiter Aggregate Root

### 7.1 `limiter/limiter.go`

```go
package limiter

import (
    "context"
    "time"

    "github.com/justinclev/slow-your-roll/clock"
    "github.com/justinclev/slow-your-roll/domain"
)

// Limiter is the aggregate root. Callers construct one per policy.
// It is safe for concurrent use.
type Limiter struct {
    policy    domain.Policy
    algorithm domain.Algorithm
    store     domain.Store
    clock     clock.Clock
}

// Option applies functional configuration to a Limiter.
type Option func(*Limiter)

// WithClock overrides the clock. Primarily used for testing.
func WithClock(c clock.Clock) Option {
    return func(l *Limiter) { l.clock = c }
}

// New constructs a Limiter. All dependencies are required; the constructor
// panics on nil inputs to fail fast at wiring time.
func New(policy domain.Policy, algo domain.Algorithm, store domain.Store, opts ...Option) *Limiter {
    if algo == nil {
        panic("ratelimiter: algorithm must not be nil")
    }
    if store == nil {
        panic("ratelimiter: store must not be nil")
    }
    l := &Limiter{
        policy:    policy,
        algorithm: algo,
        store:     store,
        clock:     clock.Real{},
    }
    for _, opt := range opts {
        opt(l)
    }
    return l
}

// Allow checks whether the given key is within rate limit bounds.
// It returns a Result describing the outcome and any store error encountered.
func (l *Limiter) Allow(ctx context.Context, key domain.Key) (domain.Result, error) {
    return l.algorithm.Allow(ctx, key, l.policy, l.store, l.clock.Now())
}

// Reset removes all stored state for the given key, returning the limiter
// to a clean slate for that subject.
func (l *Limiter) Reset(ctx context.Context, key domain.Key) error {
    return l.store.Delete(ctx, key)
}

// Policy returns the policy governing this limiter.
func (l *Limiter) Policy() domain.Policy {
    return l.policy
}
```

---

## 8. Algorithm Implementations

### 8.1 Token Bucket (`algorithm/tokenbucket/tokenbucket.go`)

```go
package tokenbucket

import (
    "context"
    "encoding/json"
    "math"
    "time"

    "github.com/justinclev/slow-your-roll/domain"
)

type state struct {
    Tokens     float64   `json:"tokens"`
    LastRefill time.Time `json:"last_refill"`
}

// Algorithm implements the token bucket algorithm.
// Tokens accumulate at a rate of Policy.Limit / Policy.Window up to Policy.Burst.
type Algorithm struct{}

func New() *Algorithm { return &Algorithm{} }

func (a *Algorithm) Allow(
    ctx context.Context,
    key domain.Key,
    policy domain.Policy,
    store domain.Store,
    now time.Time,
) (domain.Result, error) {
    entry, found, err := store.Get(ctx, key)
    if err != nil {
        return domain.Result{}, err
    }

    var s state
    if found {
        if err = json.Unmarshal(entry.Data, &s); err != nil {
            return domain.Result{}, err
        }
        // Refill tokens proportional to elapsed time.
        elapsed := now.Sub(s.LastRefill).Seconds()
        rate := float64(policy.Limit()) / policy.Window().Seconds()
        s.Tokens = math.Min(float64(policy.Burst()), s.Tokens+elapsed*rate)
    } else {
        s.Tokens = float64(policy.Burst())
    }
    s.LastRefill = now

    allowed := s.Tokens >= 1
    if allowed {
        s.Tokens--
    }

    data, err := json.Marshal(s)
    if err != nil {
        return domain.Result{}, err
    }
    if err = store.Set(ctx, key, domain.Entry{
        Data:      data,
        ExpiresAt: now.Add(policy.Window() * 2),
    }); err != nil {
        return domain.Result{}, err
    }

    resetAt := now.Add(policy.Window())
    result := domain.Result{
        Allowed:   allowed,
        Remaining: int(math.Floor(s.Tokens)),
        ResetAt:   resetAt,
    }
    if !allowed {
        refillTime := time.Duration((1 - s.Tokens) / (float64(policy.Limit()) / policy.Window().Seconds()) * float64(time.Second))
        result.RetryAfter = refillTime
    }
    return result, nil
}
```

**Fixed Window, Sliding Window Log, Sliding Window Counter** follow the same contract; their implementations are omitted from this spec for brevity but are required deliverables. Each lives in its own sub-package.

---

## 9. Store Implementations

### 9.1 In-Memory Store (`store/memory/memory.go`)

```go
package memory

import (
    "context"
    "sync"
    "time"

    "github.com/justinclev/slow-your-roll/domain"
)

type record struct {
    entry     domain.Entry
    expiresAt time.Time
}

// Store is a goroutine-safe in-memory implementation of domain.Store.
// Intended for single-process deployments and testing.
type Store struct {
    mu      sync.RWMutex
    records map[domain.Key]record
    clock   func() time.Time
}

func New() *Store {
    return &Store{
        records: make(map[domain.Key]record),
        clock:   time.Now,
    }
}

// WithClock overrides the clock. Used in tests.
func (s *Store) WithClock(fn func() time.Time) { s.clock = fn }

func (s *Store) Get(ctx context.Context, key domain.Key) (domain.Entry, bool, error) {
    s.mu.RLock()
    r, ok := s.records[key]
    s.mu.RUnlock()
    if !ok || s.clock().After(r.expiresAt) {
        return domain.Entry{}, false, nil
    }
    return r.entry, true, nil
}

func (s *Store) Set(ctx context.Context, key domain.Key, entry domain.Entry) error {
    s.mu.Lock()
    s.records[key] = record{entry: entry, expiresAt: entry.ExpiresAt}
    s.mu.Unlock()
    return nil
}

func (s *Store) Delete(ctx context.Context, key domain.Key) error {
    s.mu.Lock()
    delete(s.records, key)
    s.mu.Unlock()
    return nil
}
```

### 9.2 Redis Store (`store/redis/redis.go`)

The Redis store uses `GET` + `SET` with `EX` within a Lua script for atomicity. It depends only on `github.com/redis/go-redis/v9` and satisfies `domain.Store`.

Key requirements:
- One Lua script per algorithm type to ensure read-modify-write is atomic.
- Pipeline commands where possible to reduce RTTs.
- Context propagation for cancellation and timeouts.

---

## 10. Clock Abstraction

```go
// clock/clock.go
package clock

import "time"

// Clock abstracts time.Now() to allow deterministic tests.
type Clock interface {
    Now() time.Time
}

// Real is the production clock.
type Real struct{}
func (Real) Now() time.Time { return time.Now() }
```

```go
// internal/testclock/testclock.go
package testclock

import (
    "sync"
    "time"
)

// Clock is a controllable clock for deterministic unit tests.
type Clock struct {
    mu  sync.Mutex
    now time.Time
}

func New(initial time.Time) *Clock { return &Clock{now: initial} }

func (c *Clock) Now() time.Time {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.now
}

func (c *Clock) Advance(d time.Duration) {
    c.mu.Lock()
    c.now = c.now.Add(d)
    c.mu.Unlock()
}
```

---

## 11. HTTP Middleware

```go
// middleware/http.go
package middleware

import (
    "net/http"

    "github.com/justinclev/slow-your-roll/domain"
    "github.com/justinclev/slow-your-roll/limiter"
)

// KeyFunc extracts a rate limit key from the incoming request.
// Common implementations: IP extraction, JWT claim, API key header.
type KeyFunc func(r *http.Request) (domain.Key, error)

// OnDenied is called when the request is rejected. The default writes
// 429 Too Many Requests with Retry-After header.
type OnDenied func(w http.ResponseWriter, r *http.Request, result domain.Result)

// Handler wraps an http.Handler with rate limiting.
func Handler(l *limiter.Limiter, keyFn KeyFunc, opts ...HandlerOption) func(http.Handler) http.Handler {
    cfg := handlerConfig{onDenied: defaultOnDenied}
    for _, opt := range opts {
        opt(&cfg)
    }
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            key, err := keyFn(r)
            if err != nil {
                http.Error(w, "rate limit key error", http.StatusInternalServerError)
                return
            }
            result, err := l.Allow(r.Context(), key)
            if err != nil {
                // Fail open: let the request through on store errors.
                next.ServeHTTP(w, r)
                return
            }
            if !result.Allowed {
                cfg.onDenied(w, r, result)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

type handlerConfig struct{ onDenied OnDenied }
type HandlerOption func(*handlerConfig)

func WithOnDenied(fn OnDenied) HandlerOption {
    return func(c *handlerConfig) { c.onDenied = fn }
}

func defaultOnDenied(w http.ResponseWriter, _ *http.Request, result domain.Result) {
    w.Header().Set("Retry-After", result.RetryAfter.String())
    w.Header().Set("X-RateLimit-Remaining", "0")
    http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
}
```

---

## 12. Testing Strategy

### 12.1 Coverage Target

90% statement coverage, enforced via CI (`go test -coverprofile`).

### 12.2 Unit Test Patterns

Every algorithm test follows the same table-driven structure:

```go
func TestTokenBucket_Allow(t *testing.T) {
    clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
    store := memory.New()
    store.WithClock(clk.Now)
    algo := tokenbucket.New()

    policy, _ := domain.NewPolicy(10, time.Minute, 10)
    key := domain.Compose("user", "42")

    cases := []struct {
        name           string
        advanceBy      time.Duration
        wantAllowed    bool
        wantRemaining  int
    }{
        {name: "first request allowed", advanceBy: 0, wantAllowed: true, wantRemaining: 9},
        {name: "tenth request allowed", advanceBy: 0, wantAllowed: true, wantRemaining: 0},
        // ... exhaust burst, verify denial, advance clock, verify refill
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            clk.Advance(tc.advanceBy)
            result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if result.Allowed != tc.wantAllowed {
                t.Errorf("Allowed = %v, want %v", result.Allowed, tc.wantAllowed)
            }
            if result.Remaining != tc.wantRemaining {
                t.Errorf("Remaining = %d, want %d", result.Remaining, tc.wantRemaining)
            }
        })
    }
}
```

### 12.3 What Must Be Tested

| Component | Scenarios |
|---|---|
| `domain.Policy` | Valid construction, all three error cases |
| `domain.Key` | Empty key rejected, whitespace-only rejected, Compose output |
| Token Bucket | Allow within burst, deny at burst, refill after elapsed time |
| Fixed Window | Allow within window, deny at limit, reset on new window |
| Sliding Window Log | Fairness at boundary, expiry of old events |
| Sliding Window Counter | Approximation at boundary transition |
| Memory Store | Get miss, Get hit, Get after expiry, Set, Delete |
| Limiter | Delegates to algorithm, Reset calls store.Delete |
| HTTP Middleware | Allow passes through, deny returns 429, key error returns 500, store error fails open |

### 12.4 Integration Tests

Placed in `store/redis/redis_integration_test.go` behind build tag `//go:build integration`. Run separately in CI with a real Redis container. Not counted toward the 90% unit coverage threshold.

---

## 13. GitHub CI

### 13.1 `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Download dependencies
        run: go mod download

      - name: Run unit tests with coverage
        run: go test -race -coverprofile=coverage.out -covermode=atomic ./...

      - name: Enforce 90% coverage threshold
        run: |
          COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | tr -d '%')
          echo "Total coverage: $COVERAGE%"
          awk -v cov="$COVERAGE" 'BEGIN { if (cov+0 < 90) { print "Coverage " cov "% is below 90% threshold"; exit 1 } }'

  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Run golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  build:
    name: Build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build all packages
        run: go build ./...
```

### 13.2 `.golangci.yml` (project root)

```yaml
linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - gofmt
    - goimports
    - misspell
    - revive

linters-settings:
  revive:
    rules:
      - name: exported
      - name: error-return
      - name: error-naming
```

---

## 14. `go.mod`

```
module github.com/justinclev/slow-your-roll

go 1.22

require (
    github.com/redis/go-redis/v9 v9.x.x
    github.com/prometheus/client_golang v1.x.x
)

require (
    // indirect dependencies managed by go mod tidy
)
```

---

## 15. Public API Surface (Summary)

The library's public API is intentionally narrow. Callers interact with four entry points:

```go
// Construct a policy
policy, err := domain.NewPolicy(100, time.Minute, 100)

// Construct a limiter
l := limiter.New(policy, tokenbucket.New(), memory.New())

// Check a key
result, err := l.Allow(ctx, domain.Compose("user", userID))

// Attach to HTTP
mux.Handle("/api/", middleware.Handler(l, ipKeyFunc)(myHandler))
```

Everything else — algorithms, stores, clock — is wired at construction and hidden behind interfaces. Callers never import algorithm or store packages directly in application code; they only import them at the composition root (main or wire).

---

## 16. Extensibility Contract

Third parties may implement any of the following interfaces without forking the library:

| Interface | Location | Purpose |
|---|---|---|
| `domain.Store` | `domain/store.go` | Custom backend (DynamoDB, Postgres, etc.) |
| `domain.Algorithm` | `domain/algorithm.go` | Custom counting strategy |
| `clock.Clock` | `clock/clock.go` | Deterministic or mocked time |
| `middleware.KeyFunc` | `middleware/http.go` | Custom key extraction logic |
| `middleware.OnDenied` | `middleware/http.go` | Custom rejection response |

---

## 17. Prometheus Metrics

The library ships a `metrics` package backed by `github.com/prometheus/client_golang`. It is optional — callers who do not import `metrics` pay zero overhead.

### 17.1 `metrics/prometheus.go`

```go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/justinclev/slow-your-roll/domain"
)

// Collector wraps a domain.Algorithm and records Prometheus metrics
// for every Allow call. It satisfies domain.Algorithm so it is
// transparent to Limiter.
type Collector struct {
    inner     domain.Algorithm
    allowed   *prometheus.CounterVec
    denied    *prometheus.CounterVec
    remaining *prometheus.HistogramVec
}

// NewCollector constructs a Collector and registers its metrics with reg.
// Pass prometheus.DefaultRegisterer for the global registry.
func NewCollector(inner domain.Algorithm, reg prometheus.Registerer) (*Collector, error) {
    allowed := prometheus.NewCounterVec(prometheus.CounterOpts{
        Namespace: "ratelimiter",
        Name:      "requests_allowed_total",
        Help:      "Total number of requests allowed through the rate limiter.",
    }, []string{"key_prefix"})

    denied := prometheus.NewCounterVec(prometheus.CounterOpts{
        Namespace: "ratelimiter",
        Name:      "requests_denied_total",
        Help:      "Total number of requests denied by the rate limiter.",
    }, []string{"key_prefix"})

    remaining := prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Namespace: "ratelimiter",
        Name:      "tokens_remaining",
        Help:      "Remaining tokens/capacity at time of check.",
        Buckets:   prometheus.LinearBuckets(0, 10, 11),
    }, []string{"key_prefix"})

    for _, c := range []prometheus.Collector{allowed, denied, remaining} {
        if err := reg.Register(c); err != nil {
            return nil, err
        }
    }

    return &Collector{
        inner:     inner,
        allowed:   allowed,
        denied:    denied,
        remaining: remaining,
    }, nil
}

// Allow delegates to the inner algorithm and records the outcome.
func (c *Collector) Allow(
    ctx context.Context,
    key domain.Key,
    policy domain.Policy,
    store domain.Store,
    now time.Time,
) (domain.Result, error) {
    result, err := c.inner.Allow(ctx, key, policy, store, now)
    if err != nil {
        return result, err
    }
    prefix := keyPrefix(key)
    if result.Allowed {
        c.allowed.WithLabelValues(prefix).Inc()
    } else {
        c.denied.WithLabelValues(prefix).Inc()
    }
    c.remaining.WithLabelValues(prefix).Observe(float64(result.Remaining))
    return result, nil
}

// keyPrefix returns the first segment of a colon-namespaced key ("user:42" → "user").
// This avoids high cardinality from per-identity labels.
func keyPrefix(key domain.Key) string {
    s := string(key)
    if i := strings.Index(s, ":"); i >= 0 {
        return s[:i]
    }
    return s
}
```

### 17.2 Usage

```go
reg := prometheus.NewRegistry()

algo, err := metrics.NewCollector(tokenbucket.New(), reg)
if err != nil {
    log.Fatal(err)
}

l := limiter.New(policy, algo, store)
```

`Collector` satisfies `domain.Algorithm`, so it wraps any algorithm without changing `Limiter`'s constructor.

### 17.3 Exposed Metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `ratelimiter_requests_allowed_total` | Counter | `key_prefix` | Requests passed through |
| `ratelimiter_requests_denied_total` | Counter | `key_prefix` | Requests rejected |
| `ratelimiter_tokens_remaining` | Histogram | `key_prefix` | Remaining capacity at check time |

Labels use `key_prefix` (first colon-segment) rather than the full key to avoid unbounded cardinality.

### 17.4 What Must Be Tested

| Scenario | Expectation |
|---|---|
| Allowed request | `allowed` counter incremented, `denied` unchanged |
| Denied request | `denied` counter incremented, `allowed` unchanged |
| Inner algorithm error | Neither counter incremented, error propagated |
| `keyPrefix` single-segment key | Returns key as-is |
| `keyPrefix` multi-segment key | Returns first segment only |
| Double registration | `NewCollector` returns error, not panic |

---

## 18. Architectural Decision Records

### ADR-001: Prometheus shipped with library

**Decision:** The library ships `metrics/prometheus.go` as an opt-in package.

**Rationale:** Prometheus is the de-facto standard for Go service metrics. Bundling the adapter eliminates the boilerplate callers would otherwise duplicate across every service. It is opt-in (callers who do not import `metrics` pay zero binary or runtime cost) so it does not impose Prometheus on callers with different observability stacks.

### ADR-002: Redis Cluster is caller responsibility

**Decision:** The Redis store accepts a `*redis.Client` (or `redis.UniversalClient`) provided by the caller. The library does not configure cluster topology.

**Rationale:** Cluster configuration — sentinel addresses, TLS, authentication, connection pooling — is deployment-specific. Encoding it in the library would require exposing a large configuration surface or making opinionated choices that break in some environments. `redis.UniversalClient` already abstracts standalone, sentinel, and cluster transparently; callers wire it at their composition root.

### ADR-003: MultiLimiter deferred to v2

**Decision:** Chained per-user + per-tenant policy enforcement is out of scope for v1.

**Rationale:** `MultiLimiter` requires ordering semantics (fail-fast vs. check-all), per-policy error handling, and a merged `Result` shape. These are non-trivial design decisions that should not block shipping v1. A caller can compose multiple `Limiter` instances manually in the interim.

---

*End of Specification*
