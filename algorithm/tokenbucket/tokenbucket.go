package tokenbucket

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/your-org/ratelimiter/domain"
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
	if t, ok := store.(domain.Transactional); ok {
		var result domain.Result
		err := t.Transact(ctx, key, func(entry domain.Entry, found bool) (domain.Entry, error) {
			newEntry, r, e := compute(entry, found, policy, now)
			result = r
			return newEntry, e
		})
		return result, err
	}
	// Non-atomic fallback for stores that don't implement Transactional.
	entry, found, err := store.Get(ctx, key)
	if err != nil {
		return domain.Result{}, err
	}
	newEntry, result, err := compute(entry, found, policy, now)
	if err != nil {
		return domain.Result{}, err
	}
	return result, store.Set(ctx, key, newEntry)
}

func compute(entry domain.Entry, found bool, policy domain.Policy, now time.Time) (domain.Entry, domain.Result, error) {
	var s state
	if found {
		if err := json.Unmarshal(entry.Data, &s); err != nil {
			return domain.Entry{}, domain.Result{}, err
		}
		elapsed := now.Sub(s.LastRefill).Seconds()
		if elapsed < 0 {
			elapsed = 0 // guard against clock skew
		}
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
		return domain.Entry{}, domain.Result{}, err
	}

	newEntry := domain.Entry{Data: data, ExpiresAt: now.Add(policy.Window() * 2)}

	remaining := int(math.Floor(s.Tokens))
	if remaining < 0 {
		remaining = 0
	}
	result := domain.Result{
		Allowed:   allowed,
		Remaining: remaining,
		ResetAt:   now.Add(policy.Window()),
	}
	if !allowed {
		rate := float64(policy.Limit()) / policy.Window().Seconds()
		refillSecs := (1 - s.Tokens) / rate
		result.RetryAfter = time.Duration(refillSecs * float64(time.Second))
	}
	return newEntry, result, nil
}
