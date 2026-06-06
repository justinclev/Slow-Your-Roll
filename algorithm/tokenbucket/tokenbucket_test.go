package tokenbucket_test

import (
	"context"
	"testing"
	"time"

	"github.com/justinclev/slow-your-roll/algorithm/tokenbucket"
	"github.com/justinclev/slow-your-roll/domain"
	"github.com/justinclev/slow-your-roll/internal/testerrors"
	"github.com/justinclev/slow-your-roll/internal/testclock"
	"github.com/justinclev/slow-your-roll/store/memory"
)

func TestTokenBucket_Allow(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	key := domain.Compose("user", "42")

	// Cases run sequentially; state accumulates across rows.
	cases := []struct {
		name          string
		advanceBy     time.Duration
		wantAllowed   bool
		wantRemaining int
	}{
		{"req 1 allowed", 0, true, 9},
		{"req 2 allowed", 0, true, 8},
		{"req 3 allowed", 0, true, 7},
		{"req 4 allowed", 0, true, 6},
		{"req 5 allowed", 0, true, 5},
		{"req 6 allowed", 0, true, 4},
		{"req 7 allowed", 0, true, 3},
		{"req 8 allowed", 0, true, 2},
		{"req 9 allowed", 0, true, 1},
		{"req 10 allowed", 0, true, 0},
		{"req 11 denied", 0, false, 0},
		// After 6s: 6 * (10/60) = 1 token refilled → allowed, back to 0.
		{"refill after 6s", 6 * time.Second, true, 0},
		{"immediately denied again", 0, false, 0},
		// After full minute: burst fully restored.
		{"refill full window", time.Minute, true, 9},
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

func TestTokenBucket_RetryAfterSetOnDeny(t *testing.T) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(1, time.Minute, 1)
	key := domain.Key("retry-test")

	// Consume the single token.
	_, _ = algo.Allow(context.Background(), key, policy, store, clk.Now())

	// Next request denied; RetryAfter must be non-zero.
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

func TestTokenBucket_AllowedRetryAfterZero(t *testing.T) {
	clk := testclock.New(time.Now())
	store := memory.New()
	store.WithClock(clk.Now)
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	key := domain.Key("retry-zero")

	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if result.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0", result.RetryAfter)
	}
}

func TestTokenBucket_GetStoreError(t *testing.T) {
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.GetStore{}, time.Now())
	if err != testerrors.ErrGet {
		t.Errorf("error = %v, want ErrGet", err)
	}
}

func TestTokenBucket_SetStoreError(t *testing.T) {
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.SetStore{}, time.Now())
	if err != testerrors.ErrSet {
		t.Errorf("error = %v, want ErrSet", err)
	}
}

func TestTokenBucket_CorruptStoreData(t *testing.T) {
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.CorruptStore{}, time.Now())
	if err == nil {
		t.Error("expected unmarshal error, got nil")
	}
}

func TestTokenBucket_RemainingNeverNegative(t *testing.T) {
	clk := testclock.New(time.Now())
	store := memory.New()
	store.WithClock(clk.Now)
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(2, time.Minute, 2)
	key := domain.Key("nonneg")

	for i := 0; i < 5; i++ {
		result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Remaining < 0 {
			t.Errorf("iteration %d: Remaining = %d, want >= 0", i, result.Remaining)
		}
	}
}

func TestTokenBucket_ClockSkewGuard(t *testing.T) {
	// If now < lastRefill (clock went backwards), elapsed is clamped to 0.
	// No panic, no negative tokens; result must be valid.
	clk := testclock.New(time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := tokenbucket.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	key := domain.Key("skew-test")

	// First call at T=12:00.
	if _, err := algo.Allow(context.Background(), key, policy, store, clk.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call with now set 10s in the past (simulates clock skew).
	past := clk.Now().Add(-10 * time.Second)
	result, err := algo.Allow(context.Background(), key, policy, store, past)
	if err != nil {
		t.Fatalf("unexpected error on clock skew: %v", err)
	}
	if result.Remaining < 0 {
		t.Errorf("Remaining = %d after clock skew, want >= 0", result.Remaining)
	}
}
