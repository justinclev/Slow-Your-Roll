package fixedwindow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/justinclev/slow-your-roll/domain"
)

type state struct {
	Count       int       `json:"count"`
	WindowStart time.Time `json:"window_start"`
}

// Algorithm implements a fixed window counter.
// Each window starts when the first request arrives and resets after Policy.Window elapses.
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
		windowEnd := s.WindowStart.Add(policy.Window())
		if !now.Before(windowEnd) {
			s = state{WindowStart: now}
		}
	} else {
		s = state{WindowStart: now}
	}

	allowed := s.Count < policy.Limit()
	if allowed {
		s.Count++
	}

	data, err := json.Marshal(s)
	if err != nil {
		return domain.Entry{}, domain.Result{}, err
	}

	windowEnd := s.WindowStart.Add(policy.Window())
	newEntry := domain.Entry{Data: data, ExpiresAt: windowEnd}

	remaining := policy.Limit() - s.Count
	if remaining < 0 {
		remaining = 0
	}
	result := domain.Result{
		Allowed:   allowed,
		Remaining: remaining,
		ResetAt:   windowEnd,
	}
	if !allowed {
		result.RetryAfter = windowEnd.Sub(now)
	}
	return newEntry, result, nil
}
