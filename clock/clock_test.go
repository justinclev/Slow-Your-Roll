package clock_test

import (
	"testing"
	"time"

	"github.com/your-org/ratelimiter/clock"
)

func TestReal_Now(t *testing.T) {
	c := clock.Real{}
	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("Real.Now() = %v not in [%v, %v]", got, before, after)
	}
}

func TestReal_ImplementsClock(t *testing.T) {
	var _ clock.Clock = clock.Real{}
}
