package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/your-org/ratelimiter/domain"
	"github.com/your-org/ratelimiter/limiter"
	"github.com/your-org/ratelimiter/middleware"
	"github.com/your-org/ratelimiter/store/memory"
)

// errAlgorithm always returns an error from Allow.
type errAlgorithm struct{}

func (e *errAlgorithm) Allow(_ context.Context, _ domain.Key, _ domain.Policy, _ domain.Store, _ time.Time) (domain.Result, error) {
	return domain.Result{}, errors.New("store failure")
}

// allowAlgorithm always allows.
type allowAlgorithm struct{}

func (a *allowAlgorithm) Allow(_ context.Context, _ domain.Key, _ domain.Policy, _ domain.Store, _ time.Time) (domain.Result, error) {
	return domain.Result{Allowed: true, Remaining: 9}, nil
}

// denyAlgorithm always denies.
type denyAlgorithm struct{}

func (d *denyAlgorithm) Allow(_ context.Context, _ domain.Key, _ domain.Policy, _ domain.Store, _ time.Time) (domain.Result, error) {
	return domain.Result{Allowed: false, RetryAfter: 30 * time.Second}, nil
}

func makeKey(_ *http.Request) (domain.Key, error) {
	return domain.Key("test-key"), nil
}

func errKey(_ *http.Request) (domain.Key, error) {
	return "", errors.New("key extraction failed")
}

func newLimiter(algo domain.Algorithm) *limiter.Limiter {
	policy, _ := domain.NewPolicy(10, time.Minute, 10)
	return limiter.New(policy, algo, memory.New())
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestMiddleware_AllowPassesThrough(t *testing.T) {
	l := newLimiter(&allowAlgorithm{})
	h := middleware.Handler(l, makeKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestMiddleware_DenyReturns429(t *testing.T) {
	l := newLimiter(&denyAlgorithm{})
	h := middleware.Handler(l, makeKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
	if rr.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, want %q", rr.Header().Get("X-RateLimit-Remaining"), "0")
	}
}

func TestMiddleware_KeyErrorReturns500(t *testing.T) {
	l := newLimiter(&allowAlgorithm{})
	h := middleware.Handler(l, errKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func TestMiddleware_StoreErrorFailsOpen(t *testing.T) {
	l := newLimiter(&errAlgorithm{})
	h := middleware.Handler(l, makeKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (expected fail open)", rr.Code, http.StatusOK)
	}
}

func TestMiddleware_AllowSetsRateLimitHeaders(t *testing.T) {
	l := newLimiter(&allowAlgorithm{})
	h := middleware.Handler(l, makeKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("expected X-RateLimit-Limit header on allowed response")
	}
	if rr.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("expected X-RateLimit-Remaining header on allowed response")
	}
}

func TestMiddleware_DenySetsRateLimitHeaders(t *testing.T) {
	l := newLimiter(&denyAlgorithm{})
	h := middleware.Handler(l, makeKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("expected X-RateLimit-Limit on denied response")
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After on denied response")
	}
}

func TestMiddleware_RetryAfterIsIntegerSeconds(t *testing.T) {
	l := newLimiter(&denyAlgorithm{})
	h := middleware.Handler(l, makeKey)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	v := rr.Header().Get("Retry-After")
	// Must be parseable as integer, not float.
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			t.Errorf("Retry-After %q contains non-digit character %q", v, string(ch))
		}
	}
}

func TestMiddleware_CustomOnDenied(t *testing.T) {
	l := newLimiter(&denyAlgorithm{})
	customCalled := false
	customDeny := func(w http.ResponseWriter, _ *http.Request, _ domain.Result) {
		customCalled = true
		w.WriteHeader(http.StatusForbidden)
	}
	h := middleware.Handler(l, makeKey, middleware.WithOnDenied(customDeny))(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if !customCalled {
		t.Error("expected custom OnDenied to be called")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}
