package limiter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/your-org/ratelimiter/domain"
	"github.com/your-org/ratelimiter/internal/testclock"
	"github.com/your-org/ratelimiter/limiter"
	"github.com/your-org/ratelimiter/store/memory"
)

// stubAlgorithm records calls for assertion.
type stubAlgorithm struct {
	result domain.Result
	err    error
	calls  int
}

func (s *stubAlgorithm) Allow(_ context.Context, _ domain.Key, _ domain.Policy, _ domain.Store, _ time.Time) (domain.Result, error) {
	s.calls++
	return s.result, s.err
}

func TestLimiter_Allow_DelegatesToAlgorithm(t *testing.T) {
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	algo := &stubAlgorithm{result: domain.Result{Allowed: true, Remaining: 9}}
	store := memory.New()

	l := limiter.New(policy, algo, store)
	key := domain.Compose("user", "1")

	result, err := l.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Allowed {
		t.Error("expected Allowed = true")
	}
	if algo.calls != 1 {
		t.Errorf("algorithm called %d times, want 1", algo.calls)
	}
}

func TestLimiter_Allow_PropagatesError(t *testing.T) {
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	algo := &stubAlgorithm{err: errors.New("store failure")}
	store := memory.New()

	l := limiter.New(policy, algo, store)
	_, err := l.Allow(context.Background(), domain.Compose("user", "1"))
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLimiter_Reset_CallsStoreDelete(t *testing.T) {
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	clk := testclock.New(time.Now())
	st := memory.New()
	st.WithClock(clk.Now)

	// Pre-populate the store.
	key := domain.Compose("user", "1")
	_ = st.Set(context.Background(), key, domain.Entry{
		Data:      []byte("data"),
		ExpiresAt: clk.Now().Add(time.Hour),
	})

	_, found, _ := st.Get(context.Background(), key)
	if !found {
		t.Fatal("expected entry to exist before reset")
	}

	algo := &stubAlgorithm{}
	l := limiter.New(policy, algo, st)
	if err := l.Reset(context.Background(), key); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, found, _ = st.Get(context.Background(), key)
	if found {
		t.Error("expected entry to be deleted after reset")
	}
}

func TestLimiter_Policy_Returns(t *testing.T) {
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	algo := &stubAlgorithm{}
	l := limiter.New(policy, algo, memory.New())

	if l.Policy().Limit() != 10 {
		t.Errorf("Policy().Limit() = %d, want 10", l.Policy().Limit())
	}
}

func TestLimiter_New_PanicsOnNilAlgorithm(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil algorithm")
		}
	}()
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	limiter.New(policy, nil, memory.New())
}

func TestLimiter_New_PanicsOnNilStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil store")
		}
	}()
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	limiter.New(policy, &stubAlgorithm{}, nil)
}

func TestLimiter_WithClock(t *testing.T) {
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	fixed := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := testclock.New(fixed)

	var capturedNow time.Time
	algo := &captureClockAlgo{}
	l := limiter.New(policy, algo, memory.New(), limiter.WithClock(clk))
	_, _ = l.Allow(context.Background(), domain.Compose("u", "1"))
	capturedNow = algo.capturedNow

	if !capturedNow.Equal(fixed) {
		t.Errorf("clock passed to algorithm = %v, want %v", capturedNow, fixed)
	}
}

type captureClockAlgo struct {
	capturedNow time.Time
}

func (a *captureClockAlgo) Allow(_ context.Context, _ domain.Key, _ domain.Policy, _ domain.Store, now time.Time) (domain.Result, error) {
	a.capturedNow = now
	return domain.Result{Allowed: true}, nil
}
