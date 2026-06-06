package domain

import (
	"errors"
	"time"
)

// Policy is an immutable value object describing how many requests
// are allowed within a given window, and how much burst is permitted.
type Policy struct {
	limit  int
	window time.Duration
	burst  int
}

var (
	ErrInvalidLimit  = errors.New("limit must be greater than zero")
	ErrInvalidWindow = errors.New("window must be greater than zero")
	ErrInvalidBurst  = errors.New("burst must be >= limit")
)

// NewPolicy constructs a validated Policy.
// burst is the maximum instantaneous allowance; it must be >= limit.
func NewPolicy(limit int, window time.Duration, burst int) (Policy, error) {
	if limit <= 0 {
		return Policy{}, ErrInvalidLimit
	}
	if window <= 0 {
		return Policy{}, ErrInvalidWindow
	}
	if burst < limit {
		return Policy{}, ErrInvalidBurst
	}
	return Policy{limit: limit, window: window, burst: burst}, nil
}

func (p Policy) Limit() int            { return p.limit }
func (p Policy) Window() time.Duration { return p.window }
func (p Policy) Burst() int            { return p.burst }

// WithLimit returns a new Policy with the given limit.
func (p Policy) WithLimit(limit int) (Policy, error) {
	return NewPolicy(limit, p.window, p.burst)
}

// WithWindow returns a new Policy with the given window.
func (p Policy) WithWindow(window time.Duration) (Policy, error) {
	return NewPolicy(p.limit, window, p.burst)
}

// WithBurst returns a new Policy with the given burst.
func (p Policy) WithBurst(burst int) (Policy, error) {
	return NewPolicy(p.limit, p.window, burst)
}
