package slidingwindowcounter_test

import (
	"context"
	"testing"
	"time"

	"github.com/justinclev/slow-your-roll/algorithm/slidingwindowcounter"
	"github.com/justinclev/slow-your-roll/domain"
	"github.com/justinclev/slow-your-roll/internal/testerrors"
	"github.com/justinclev/slow-your-roll/internal/testclock"
	"github.com/justinclev/slow-your-roll/store/memory"
)

func TestSlidingWindowCounter_AllowWithinLimit(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	key := domain.Compose("user", "swc")

	for i := 0; i < 5; i++ {
		result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if err != nil {
			t.Fatalf("req %d: unexpected error: %v", i, err)
		}
		if !result.Allowed {
			t.Errorf("req %d: expected allowed", i)
		}
	}
}

func TestSlidingWindowCounter_DenyAtLimit(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(3, time.Minute, 3)
	key := domain.Compose("user", "swc-deny")

	for i := 0; i < 3; i++ {
		algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	}

	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied at limit")
	}
}

func TestSlidingWindowCounter_ApproximationAtBoundaryTransition(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowcounter.New()
	// Limit 10 per minute.
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	key := domain.Compose("user", "boundary")

	// 8 requests in first window.
	for i := 0; i < 8; i++ {
		algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	}

	// Move to start of new window (previous window had 8 requests).
	// At 30s into new window: weight = 0.5, weighted = 8*0.5 + 0 = 4.
	// Should allow up to 10-4=6 more.
	clk.Advance(time.Minute + 30*time.Second)

	allowed := 0
	for i := 0; i < 10; i++ {
		result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if err != nil {
			t.Fatalf("req %d: unexpected error: %v", i, err)
		}
		if result.Allowed {
			allowed++
		}
	}
	// With 8 previous and 0.5 weight = 4 effective, should allow ~6.
	if allowed < 5 || allowed > 7 {
		t.Errorf("allowed = %d at boundary transition, want 5-7 (approximation)", allowed)
	}
}

func TestSlidingWindowCounter_FullWindowReset(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(2, time.Minute, 2)
	key := domain.Compose("user", "reset")

	// Exhaust limit.
	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck

	// Advance two full windows: state resets completely.
	clk.Advance(2 * time.Minute)
	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed after two full windows elapsed")
	}
}

func TestSlidingWindowCounter_RemainingNeverNegative(t *testing.T) {
	clk := testclock.New(time.Now())
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(2, time.Minute, 2)
	key := domain.Key("neg-check")

	for i := 0; i < 5; i++ {
		result, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if result.Remaining < 0 {
			t.Errorf("iteration %d: Remaining = %d, want >= 0", i, result.Remaining)
		}
	}
}

func TestSlidingWindowCounter_GetStoreError(t *testing.T) {
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.GetStore{}, time.Now())
	if err != testerrors.ErrGet {
		t.Errorf("error = %v, want ErrGet", err)
	}
}

func TestSlidingWindowCounter_SetStoreError(t *testing.T) {
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.SetStore{}, time.Now())
	if err != testerrors.ErrSet {
		t.Errorf("error = %v, want ErrSet", err)
	}
}

func TestSlidingWindowCounter_CorruptStoreData(t *testing.T) {
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.CorruptStore{}, time.Now())
	if err == nil {
		t.Error("expected unmarshal error, got nil")
	}
}

func TestSlidingWindowCounter_RetryAfterOnDeny(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowcounter.New()
	policy, _ := domain.NewPolicy(1, time.Minute, 1)
	key := domain.Key("retry")

	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected denied")
	}
	if result.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", result.RetryAfter)
	}
}
