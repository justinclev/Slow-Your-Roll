package metrics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/your-org/ratelimiter/domain"
	"github.com/your-org/ratelimiter/metrics"
	"github.com/your-org/ratelimiter/store/memory"
)

// stubAlgo controls Allow return values.
type stubAlgo struct {
	result domain.Result
	err    error
}

func (s *stubAlgo) Allow(_ context.Context, _ domain.Key, _ domain.Policy, _ domain.Store, _ time.Time) (domain.Result, error) {
	return s.result, s.err
}

func counterValue(t *testing.T, reg *prometheus.Registry, name, label string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "key_prefix" && lp.GetValue() == label {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func histogramCount(t *testing.T, reg *prometheus.Registry, name, label string) uint64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "key_prefix" && lp.GetValue() == label {
					return m.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	return 0
}

func newCollectorAndReg(t *testing.T, algo domain.Algorithm) (*metrics.Collector, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	c, err := metrics.NewCollector(algo, reg)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c, reg
}

func TestCollector_AllowedIncrementsAllowed(t *testing.T) {
	algo := &stubAlgo{result: domain.Result{Allowed: true, Remaining: 5}}
	c, reg := newCollectorAndReg(t, algo)
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	store := memory.New()
	key := domain.Compose("user", "1")

	_, err := c.Allow(context.Background(), key, policy, store, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if v := counterValue(t, reg, "ratelimiter_requests_allowed_total", "user"); v != 1 {
		t.Errorf("allowed counter = %v, want 1", v)
	}
	if v := counterValue(t, reg, "ratelimiter_requests_denied_total", "user"); v != 0 {
		t.Errorf("denied counter = %v, want 0", v)
	}
}

func TestCollector_DeniedIncrementsDenied(t *testing.T) {
	algo := &stubAlgo{result: domain.Result{Allowed: false, RetryAfter: 5 * time.Second}}
	c, reg := newCollectorAndReg(t, algo)
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	store := memory.New()
	key := domain.Compose("user", "2")

	_, err := c.Allow(context.Background(), key, policy, store, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if v := counterValue(t, reg, "ratelimiter_requests_denied_total", "user"); v != 1 {
		t.Errorf("denied counter = %v, want 1", v)
	}
	if v := counterValue(t, reg, "ratelimiter_requests_allowed_total", "user"); v != 0 {
		t.Errorf("allowed counter = %v, want 0", v)
	}
}

func TestCollector_InnerError_NeitherCounterIncremented(t *testing.T) {
	algo := &stubAlgo{err: errors.New("boom")}
	c, reg := newCollectorAndReg(t, algo)
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	store := memory.New()
	key := domain.Compose("user", "3")

	_, err := c.Allow(context.Background(), key, policy, store, time.Now())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if v := counterValue(t, reg, "ratelimiter_requests_allowed_total", "user"); v != 0 {
		t.Errorf("allowed counter = %v, want 0 on error", v)
	}
	if v := counterValue(t, reg, "ratelimiter_requests_denied_total", "user"); v != 0 {
		t.Errorf("denied counter = %v, want 0 on error", v)
	}
}

func TestCollector_RemainingObserved(t *testing.T) {
	algo := &stubAlgo{result: domain.Result{Allowed: true, Remaining: 7}}
	c, reg := newCollectorAndReg(t, algo)
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	store := memory.New()
	key := domain.Compose("tenant", "acme")

	_, _ = c.Allow(context.Background(), key, policy, store, time.Now())

	if cnt := histogramCount(t, reg, "ratelimiter_tokens_remaining", "tenant"); cnt != 1 {
		t.Errorf("histogram sample count = %d, want 1", cnt)
	}
}

func TestCollector_DoubleRegistration_ReturnsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	algo := &stubAlgo{}
	_, err := metrics.NewCollector(algo, reg)
	if err != nil {
		t.Fatalf("first NewCollector: %v", err)
	}
	_, err = metrics.NewCollector(algo, reg)
	if err == nil {
		t.Error("expected error on double registration, got nil")
	}
}

func TestKeyPrefix_SingleSegment(t *testing.T) {
	algo := &stubAlgo{result: domain.Result{Allowed: true}}
	c, reg := newCollectorAndReg(t, algo)
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	store := memory.New()

	// Key with no colon: prefix == full key.
	key := domain.Key("singlekey")
	_, _ = c.Allow(context.Background(), key, policy, store, time.Now())

	if v := counterValue(t, reg, "ratelimiter_requests_allowed_total", "singlekey"); v != 1 {
		t.Errorf("single-segment key prefix counter = %v, want 1", v)
	}
}

func TestNewCollector_NilInner_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil inner algorithm")
		}
	}()
	reg := prometheus.NewRegistry()
	_, _ = metrics.NewCollector(nil, reg)
}

func TestKeyPrefix_MultiSegment(t *testing.T) {
	algo := &stubAlgo{result: domain.Result{Allowed: true}}
	c, reg := newCollectorAndReg(t, algo)
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	store := memory.New()

	// "org:team:user" → prefix "org".
	key := domain.Key("org:team:user")
	_, _ = c.Allow(context.Background(), key, policy, store, time.Now())

	if v := counterValue(t, reg, "ratelimiter_requests_allowed_total", "org"); v != 1 {
		t.Errorf("multi-segment key prefix counter = %v, want 1", v)
	}

	// Ensure full key is NOT used as label.
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != "ratelimiter_requests_allowed_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "key_prefix" && lp.GetValue() == "org:team:user" {
					t.Error("full key used as label, want first segment only")
				}
			}
		}
	}
	_ = dto.MetricFamily{}
}
