package middleware

import (
	"net"
	"net/http"
	"strconv"

	"github.com/justinclev/slow-your-roll/domain"
	"github.com/justinclev/slow-your-roll/limiter"
)

// KeyFunc extracts a rate limit key from the incoming request.
// Common implementations: IP extraction, JWT claim, API key header.
type KeyFunc func(r *http.Request) (domain.Key, error)

// IPKeyFunc is a KeyFunc that uses the client IP from r.RemoteAddr as the key.
// For requests behind a reverse proxy, use HeaderKeyFunc("X-Forwarded-For") or
// HeaderKeyFunc("X-Real-IP") instead.
func IPKeyFunc(r *http.Request) (domain.Key, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return domain.NewKey(host)
}

// HeaderKeyFunc returns a KeyFunc that reads the named HTTP header as the key.
// Returns an error if the header is absent or empty, which causes the middleware
// to respond with 500. Use this for API-key or tenant-ID based limiting.
func HeaderKeyFunc(header string) KeyFunc {
	return func(r *http.Request) (domain.Key, error) {
		return domain.NewKey(r.Header.Get(header))
	}
}

// OnDenied is called when the request is rejected. The default writes
// 429 Too Many Requests with standard rate limit headers.
type OnDenied func(w http.ResponseWriter, r *http.Request, result domain.Result)

type handlerConfig struct{ onDenied OnDenied }

// HandlerOption configures optional behaviour of the rate limit middleware.
type HandlerOption func(*handlerConfig)

// WithOnDenied overrides the default 429 response handler.
func WithOnDenied(fn OnDenied) HandlerOption {
	return func(c *handlerConfig) { c.onDenied = fn }
}

// Handler wraps an http.Handler with rate limiting.
// Standard rate limit headers (X-RateLimit-Limit, X-RateLimit-Remaining,
// X-RateLimit-Reset) are set on every non-error response.
// On store errors the middleware fails open (lets the request through).
func Handler(l *limiter.Limiter, keyFn KeyFunc, opts ...HandlerOption) func(http.Handler) http.Handler {
	cfg := handlerConfig{onDenied: defaultOnDenied}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, err := keyFn(r)
			if err != nil {
				http.Error(w, "rate limit key error", http.StatusInternalServerError)
				return
			}
			result, err := l.Allow(r.Context(), key)
			if err != nil {
				// Fail open: let the request through on store errors.
				next.ServeHTTP(w, r)
				return
			}
			setRateLimitHeaders(w, l.Policy(), result)
			if !result.Allowed {
				cfg.onDenied(w, r, result)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// setRateLimitHeaders writes standard rate limit headers to w.
// Called on every successful Allow check (both allowed and denied).
func setRateLimitHeaders(w http.ResponseWriter, policy domain.Policy, result domain.Result) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(policy.Limit()))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	if !result.ResetAt.IsZero() {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
	}
}

func defaultOnDenied(w http.ResponseWriter, _ *http.Request, result domain.Result) {
	// Retry-After per RFC 7231 §7.1.3: integer number of seconds.
	retryAfterSecs := int(result.RetryAfter.Seconds())
	if retryAfterSecs < 1 && result.RetryAfter > 0 {
		retryAfterSecs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSecs))
	http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
}
