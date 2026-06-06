package domain

import "time"

// Result is an immutable value object returned by a rate limit check.
type Result struct {
	Allowed    bool
	Remaining  int
	ResetAt    time.Time
	RetryAfter time.Duration // zero when Allowed == true
}
