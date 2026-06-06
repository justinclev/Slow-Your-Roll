package testclock

import (
	"sync"
	"time"
)

// Clock is a controllable clock for deterministic unit tests.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func New(initial time.Time) *Clock { return &Clock{now: initial} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
