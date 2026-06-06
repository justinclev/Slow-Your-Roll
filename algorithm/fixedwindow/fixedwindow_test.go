package fixedwindow_test

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/ratelimiter/algorithm/fixedwindow"
	"github.com/your-org/ratelimiter/domain"
	"github.com/your-org/ratelimiter/internal/testerrors"
	"github.com/your-org/ratelimiter/internal/testclock"
	"github.com/your-org/ratelimiter/store/memory"
)

func setup(limit int, window time.Duration) (*testclock.Clock, *memory.Store, *fixedwindow.Algorithm, domain.Policy, domain.Key) {
	clk := testclock.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	store := memory.New()
	store.WithClock(clk.Now)
	algo := fixedwindow.New()
	policy, _ := domain.NewPolicy(limit, window, limit)
	key := domain.Compose("user", "fw")
	return clk, store, algo, policy, key
}

func TestFixedWindow_AllowWithinWindow(t *testing.T) {
	clk, store, algo, policy, key := setup(3, time.Minute)

	cases := []struct {
		name          string
		wantAllowed   bool
		wantRemaining int
	}{
		{"req 1", true, 2},
		{"req 2", true, 1},
		{"req 3", true, 0},
		{"req 4 denied", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = clk
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

func TestFixedWindow_ResetOnNewWindow(t *testing.T) {
	clk, store, algo, policy, key := setup(2, time.Minute)

	// Exhaust window.
	for i := 0; i < 2; i++ {
		algo.Allow(context.Background(), key, policy, store, clk.Now()) //nolint:errcheck
	}
	result, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if result.Allowed {
		t.Fatal("expected denied before window reset")
	}

	// Advance into new window.
	clk.Advance(time.Minute)
	result, err := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected allowed after window reset")
	}
	if result.Remaining != 1 {
		t.Errorf("Remaining = %d, want 1", result.Remaining)
	}
}

func TestFixedWindow_RetryAfterOnDeny(t *testing.T) {
	clk, store, algo, policy, key := setup(1, time.Minute)

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

func TestFixedWindow_AllowedRetryAfterZero(t *testing.T) {
	clk, store, algo, policy, key := setup(5, time.Minute)
	result, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
	if result.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 on allowed request", result.RetryAfter)
	}
}

func TestFixedWindow_GetStoreError(t *testing.T) {
	algo := fixedwindow.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.GetStore{}, time.Now())
	if err != testerrors.ErrGet {
		t.Errorf("error = %v, want ErrGet", err)
	}
}

func TestFixedWindow_SetStoreError(t *testing.T) {
	algo := fixedwindow.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.SetStore{}, time.Now())
	if err != testerrors.ErrSet {
		t.Errorf("error = %v, want ErrSet", err)
	}
}

func TestFixedWindow_CorruptStoreData(t *testing.T) {
	algo := fixedwindow.New()
	policy, _ := domain.NewPolicy(5, time.Minute, 5)
	_, err := algo.Allow(context.Background(), domain.Key("k"), policy, testerrors.CorruptStore{}, time.Now())
	if err == nil {
		t.Error("expected unmarshal error, got nil")
	}
}

func TestFixedWindow_RemainingNeverNegative(t *testing.T) {
	clk, store, algo, policy, key := setup(2, time.Minute)
	for i := 0; i < 5; i++ {
		result, _ := algo.Allow(context.Background(), key, policy, store, clk.Now())
		if result.Remaining < 0 {
			t.Errorf("iteration %d: Remaining = %d, want >= 0", i, result.Remaining)
		}
	}
}
