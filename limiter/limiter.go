package limiter

import (
	"context"

	"github.com/your-org/ratelimiter/clock"
	"github.com/your-org/ratelimiter/domain"
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
