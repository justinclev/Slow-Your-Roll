package slidingwindowcounter

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/your-org/ratelimiter/domain"
)

type state struct {
	CurrentCount  int       `json:"current_count"`
	PreviousCount int       `json:"previous_count"`
	WindowStart   time.Time `json:"window_start"`
}

// Algorithm implements the sliding window counter algorithm.
// It approximates a sliding log using two fixed windows:
//
//	weightedCount = previousCount * (1 - elapsed/window) + currentCount
//
// Lower memory than sliding window log at the cost of approximate fairness.
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
			elapsed := now.Sub(windowEnd)
			if elapsed >= policy.Window() {
				s = state{WindowStart: now}
			} else {
				s.PreviousCount = s.CurrentCount
				s.CurrentCount = 0
				s.WindowStart = windowEnd
			}
		}
	} else {
		s = state{WindowStart: now}
	}

	elapsed := now.Sub(s.WindowStart)
	weight := 1.0 - elapsed.Seconds()/policy.Window().Seconds()
	if weight < 0 {
		weight = 0
	}
	weightedCount := float64(s.PreviousCount)*weight + float64(s.CurrentCount)

	allowed := weightedCount < float64(policy.Limit())
	if allowed {
		s.CurrentCount++
		weightedCount++ // reflect the just-consumed token in remaining calc
	}

	data, err := json.Marshal(s)
	if err != nil {
		return domain.Entry{}, domain.Result{}, err
	}

	windowEnd := s.WindowStart.Add(policy.Window())
	newEntry := domain.Entry{Data: data, ExpiresAt: windowEnd.Add(policy.Window())}

	remaining := policy.Limit() - int(math.Ceil(weightedCount))
	if remaining < 0 {
		remaining = 0
	}
	result := domain.Result{
		Allowed:   allowed,
		Remaining: remaining,
		ResetAt:   windowEnd,
	}
	if !allowed {
		retryAfter := windowEnd.Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		result.RetryAfter = retryAfter
	}
	return newEntry, result, nil
}
