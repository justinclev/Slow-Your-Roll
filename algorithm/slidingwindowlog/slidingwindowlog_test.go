package slidingwindowlog_test

import (
	"context"
	"testing"
	"time"

	"github.com/justinclev/slow-your-roll/algorithm/slidingwindowlog"
	"github.com/justinclev/slow-your-roll/domain"
	"github.com/justinclev/slow-your-roll/internal/testerrors"
	"github.com/justinclev/slow-your-roll/internal/testclock"
	"github.com/justinclev/slow-your-roll/store/memory"
)

func TestSlidingWindowLog_AllowWithinLimit(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(3, time.Minute, 3)
	key := domain.Compose("user", "swl")

	for i := 0; i < 3; i++ {
		result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if err != nil {
			t.Fatalf("req %d: unexpected error: %v", i, err)
		}
		if !result.Allowed {
			t.Errorf("req %d: expected allowed", i)
		}
	}
}

func TestSlidingWindowLog_DenyAtLimit(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(2, time.Minute, 2)
	key := domain.Compose("user", "swl-deny")

	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck

	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Allowed {
		t.Error("expected denied at limit")
	}
	if result.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", result.RetryAfter)
	}
}

func TestSlidingWindowLog_FairnessAtBoundary(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(2, time.Minute, 2)
	key := domain.Compose("user", "boundary")

	// Two requests at T=0.
	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck

	// At T=59s: still within window, denied.
	clk.Advance(59 * time.Second)
	result, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if result.Allowed {
		t.Error("expected denied at T=59s (both requests still in window)")
	}

	// At T=61s: both T=0 requests have expired, 2 slots open.
	clk.Advance(2 * time.Second)
	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed at T=61s (old requests expired)")
	}
}

func TestSlidingWindowLog_ExpiryOfOldEvents(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(1, time.Minute, 1)
	key := domain.Compose("user", "expiry")

	// Use the single slot.
	r, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if !r.Allowed {
		t.Fatal("first request should be allowed")
	}

	// Advance past window: old event expires.
	clk.Advance(61 * time.Second)
	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed after old event expired")
	}
}

func TestSlidingWindowLog_GetStoreError(t *testing.T) {
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.GetStore{}, time.Now())
	if err != testerrors.ErrGet {
		t.Errorf("error = %v, want ErrGet", err)
	}
}

func TestSlidingWindowLog_SetStoreError(t *testing.T) {
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.SetStore{}, time.Now())
	if err != testerrors.ErrSet {
		t.Errorf("error = %v, want ErrSet", err)
	}
}

func TestSlidingWindowLog_CorruptStoreData(t *testing.T) {
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.CorruptStore{}, time.Now())
	if err == nil {
		t.Error("expected unmarshal error, got nil")
	}
}

func TestSlidingWindowLog_RemainingNeverNegative(t *testing.T) {
	clk := testclock.New(time.Now())
	store := memory.New()
	store.WithClock(clk.Now)
	algo := slidingwindowlog.New()
	policy, _ := domain.NewPolicy(2, time.Minute, 2)
	key := domain.Key("neg-check")

	for i := 0; i < 5; i++ {
		result, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if result.Remaining < 0 {
			t.Errorf("iteration %d: Remaining = %d, want >= 0", i, result.Remaining)
		}
	}
}
