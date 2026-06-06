package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/your-org/ratelimiter/domain"
)

// Collector wraps a domain.Algorithm and records Prometheus metrics
// for every Allow call. It satisfies domain.Algorithm so it is
// transparent to Limiter.
type Collector struct {
	inner     domain.Algorithm
	allowed   *prometheus.CounterVec
	denied    *prometheus.CounterVec
	remaining *prometheus.HistogramVec
}

// NewCollector constructs a Collector and registers its metrics with reg.
// Pass prometheus.DefaultRegisterer for the global registry.
// Panics if inner is nil.
func NewCollector(inner domain.Algorithm, reg prometheus.Registerer) (*Collector, error) {
	if inner == nil {
		panic("ratelimiter/metrics: inner algorithm must not be nil")
	}

	allowed := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ratelimiter",
		Name:      "requests_allowed_total",
		Help:      "Total number of requests allowed through the rate limiter.",
	}, []string{"key_prefix"})

	denied := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ratelimiter",
		Name:      "requests_denied_total",
		Help:      "Total number of requests denied by the rate limiter.",
	}, []string{"key_prefix"})

	remaining := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ratelimiter",
		Name:      "tokens_remaining",
		Help:      "Remaining tokens/capacity at time of check.",
		Buckets:   prometheus.LinearBuckets(0, 10, 11),
	}, []string{"key_prefix"})

	for _, c := range []prometheus.Collector{allowed, denied, remaining} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}

	return &Collector{
		inner:     inner,
		allowed:   allowed,
		denied:    denied,
		remaining: remaining,
	}, nil
}

// Allow delegates to the inner algorithm and records the outcome.
func (c *Collector) Allow(
	ctx context.Context,
	key domain.Key,
	policy domain.Policy,
	store domain.Store,
	now time.Time,
) (domain.Result, error) {
	result, err := c.inner.Allow(ctx, key, policy, store, now)
	if err != nil {
		return result, err
	}
	prefix := keyPrefix(key)
	if result.Allowed {
		c.allowed.WithLabelValues(prefix).Inc()
	} else {
		c.denied.WithLabelValues(prefix).Inc()
	}
	c.remaining.WithLabelValues(prefix).Observe(float64(result.Remaining))
	return result, nil
}

// keyPrefix returns the first colon-segment of key to avoid high cardinality labels.
// "user:42" → "user", "tenant" → "tenant".
func keyPrefix(key domain.Key) string {
	s := string(key)
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}
