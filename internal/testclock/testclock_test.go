package testclock_test

import (
	"testing"
	"time"

	"github.com/your-org/ratelimiter/internal/testclock"
)

var epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func TestClock_New(t *testing.T) {
	c := testclock.New(epoch)
	if !c.Now().Equal(epoch) {
		t.Errorf("Now() = %v, want %v", c.Now(), epoch)
	}
}

func TestClock_Advance(t *testing.T) {
	c := testclock.New(epoch)
	c.Advance(time.Hour)
	want := epoch.Add(time.Hour)
	if !c.Now().Equal(want) {
		t.Errorf("Now() after Advance = %v, want %v", c.Now(), want)
	}
}

func TestClock_AdvanceMultiple(t *testing.T) {
	c := testclock.New(epoch)
	c.Advance(time.Minute)
	c.Advance(time.Minute)
	want := epoch.Add(2 * time.Minute)
	if !c.Now().Equal(want) {
		t.Errorf("Now() = %v, want %v", c.Now(), want)
	}
}
