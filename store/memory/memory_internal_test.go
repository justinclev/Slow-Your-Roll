package memory

import (
	"context"
	"testing"
	"time"

	"github.com/justinclev/slow-your-roll/domain"
)

// evictExpired is unexported; test it directly from the same package.

func TestEvictExpired_RemovesExpiredEntries(t *testing.T) {
	clk := time.Now()
	s := New()
	s.WithClock(func() time.Time { return clk })

	active := domain.Key("active")
	expired := domain.Key("expired")

	_ = s.Set(context.Background(), active, domain.Entry{
		Data:      []byte("keep"),
		ExpiresAt: clk.Add(time.Hour),
	})
	_ = s.Set(context.Background(), expired, domain.Entry{
		Data:      []byte("gone"),
		ExpiresAt: clk.Add(time.Second),
	})

	// Advance clock past expiry of "expired" only.
	clk = clk.Add(2 * time.Second)

	s.evictExpired()

	s.mu.RLock()
	_, hasActive := s.records[active]
	_, hasExpired := s.records[expired]
	s.mu.RUnlock()

	if !hasActive {
		t.Error("evictExpired removed a non-expired entry")
	}
	if hasExpired {
		t.Error("evictExpired did not remove an expired entry")
	}
}

func TestEvictExpired_EmptyStore(t *testing.T) {
	s := New()
	// Must not panic on empty store.
	s.evictExpired()
	s.Close()
}
