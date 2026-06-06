package slidingwindowlog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/justinclev/slow-your-roll/domain"
)

// state holds Unix-nanosecond timestamps of requests within the current window.
type state struct {
	Timestamps []int64 `json:"timestamps"`
}

// Algorithm implements the sliding window log algorithm.
// Every request timestamp is recorded; old entries are pruned on each call.
// Memory cost is O(limit) per key.
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
	}

	cutoff := now.Add(-policy.Window()).UnixNano()
	// Filter in-place: write position always <= read position so no aliasing issue.
	valid := s.Timestamps[:0]
	for _, ts := range s.Timestamps {
		if ts > cutoff {
			valid = append(valid, ts)
		}
	}
	s.Timestamps = valid

	allowed := len(s.Timestamps) < policy.Limit()
	if allowed {
		s.Timestamps = append(s.Timestamps, now.UnixNano())
	}

	data, err := json.Marshal(s)
	if err != nil {
		return domain.Entry{}, domain.Result{}, err
	}

	newEntry := domain.Entry{Data: data, ExpiresAt: now.Add(policy.Window())}

	remaining := policy.Limit() - len(s.Timestamps)
	if remaining < 0 {
		remaining = 0
	}

	var resetAt time.Time
	var retryAfter time.Duration
	if len(s.Timestamps) > 0 {
		oldest := time.Unix(0, s.Timestamps[0])
		resetAt = oldest.Add(policy.Window())
		if !allowed {
			retryAfter = resetAt.Sub(now)
			if retryAfter < 0 {
				retryAfter = 0
			}
		}
	} else {
		resetAt = now.Add(policy.Window())
	}

	return newEntry, domain.Result{
		Allowed:    allowed,
		Remaining:  remaining,
		ResetAt:    resetAt,
		RetryAfter: retryAfter,
	}, nil
}
