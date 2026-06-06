package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/your-org/ratelimiter/domain"
	"github.com/your-org/ratelimiter/internal/testclock"
	"github.com/your-org/ratelimiter/store/memory"
)

func TestStore_GetMiss(t *testing.T) {
	s := memory.New()
	_, found, err := s.Get(context.Background(), domain.Key("missing"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found = false on miss")
	}
}

func TestStore_SetAndGetHit(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("test-key")
	entry := domain.Entry{
		Data:      []byte("hello"),
		ExpiresAt: clk.Now().Add(time.Hour),
	}
	if err := s.Set(context.Background(), key, entry); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, found, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found = true")
	}
	if string(got.Data) != "hello" {
		t.Errorf("Data = %q, want %q", got.Data, "hello")
	}
}

func TestStore_GetAfterExpiry(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("expiry-key")
	entry := domain.Entry{
		Data:      []byte("data"),
		ExpiresAt: clk.Now().Add(time.Second),
	}
	_ = s.Set(context.Background(), key, entry)

	// Advance past expiry.
	clk.Advance(2 * time.Second)

	_, found, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found = false after expiry")
	}
}

func TestStore_Delete(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("delete-key")
	entry := domain.Entry{
		Data:      []byte("data"),
		ExpiresAt: clk.Now().Add(time.Hour),
	}
	_ = s.Set(context.Background(), key, entry)

	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, found, _ := s.Get(context.Background(), key)
	if found {
		t.Error("expected found = false after delete")
	}
}

func TestStore_DeleteIdempotent(t *testing.T) {
	s := memory.New()
	key := domain.Key("nonexistent")
	// Should not error when deleting a missing key.
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatalf("unexpected error on idempotent delete: %v", err)
	}
}

func TestStore_SetOverwrites(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("overwrite-key")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("first"), ExpiresAt: clk.Now().Add(time.Hour)})
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("second"), ExpiresAt: clk.Now().Add(time.Hour)})

	got, found, _ := s.Get(context.Background(), key)
	if !found {
		t.Fatal("expected found = true")
	}
	if string(got.Data) != "second" {
		t.Errorf("Data = %q, want %q", got.Data, "second")
	}
}

func TestStore_Close(t *testing.T) {
	s := memory.New()
	// Close must not panic or block.
	s.Close()
}

func TestStore_Transact_Miss(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("txn-miss")
	err := s.Transact(context.Background(), key, func(entry domain.Entry, found bool) (domain.Entry, error) {
		if found {
			t.Error("expected found = false on miss")
		}
		return domain.Entry{Data: []byte("new"), ExpiresAt: clk.Now().Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok, _ := s.Get(context.Background(), key)
	if !ok {
		t.Fatal("expected entry written by Transact")
	}
	if string(got.Data) != "new" {
		t.Errorf("Data = %q, want %q", got.Data, "new")
	}
}

func TestStore_Transact_Hit(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("txn-hit")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("old"), ExpiresAt: clk.Now().Add(time.Hour)})

	err := s.Transact(context.Background(), key, func(entry domain.Entry, found bool) (domain.Entry, error) {
		if !found {
			t.Error("expected found = true on hit")
		}
		if string(entry.Data) != "old" {
			t.Errorf("entry.Data = %q, want %q", entry.Data, "old")
		}
		return domain.Entry{Data: []byte("updated"), ExpiresAt: clk.Now().Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok, _ := s.Get(context.Background(), key)
	if !ok || string(got.Data) != "updated" {
		t.Errorf("Data = %q, want %q", got.Data, "updated")
	}
}

func TestStore_Transact_ExpiredTreatedAsMiss(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("txn-expired")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("stale"), ExpiresAt: clk.Now().Add(time.Second)})
	clk.Advance(2 * time.Second)

	err := s.Transact(context.Background(), key, func(entry domain.Entry, found bool) (domain.Entry, error) {
		if found {
			t.Error("expected found = false for expired entry")
		}
		return domain.Entry{Data: []byte("fresh"), ExpiresAt: clk.Now().Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStore_Transact_FnError_DoesNotWrite(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	key := domain.Key("txn-fn-err")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("original"), ExpiresAt: clk.Now().Add(time.Hour)})

	fnErr := errors.New("fn failed")
	err := s.Transact(context.Background(), key, func(_ domain.Entry, _ bool) (domain.Entry, error) {
		return domain.Entry{}, fnErr
	})
	if err != fnErr {
		t.Errorf("error = %v, want %v", err, fnErr)
	}

	// Original entry must be unchanged.
	got, ok, _ := s.Get(context.Background(), key)
	if !ok || string(got.Data) != "original" {
		t.Errorf("entry was modified on fn error: Data = %q", got.Data)
	}
}

func TestStore_EvictExpiredViaClose(t *testing.T) {
	clk := testclock.New(time.Now())
	s := memory.New()
	s.WithClock(clk.Now)

	// Set entry, advance past expiry, verify Get returns miss (lazy eviction).
	key := domain.Key("evict-key")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("x"), ExpiresAt: clk.Now().Add(time.Second)})
	clk.Advance(2 * time.Second)

	_, found, _ := s.Get(context.Background(), key)
	if found {
		t.Error("expected found = false after expiry")
	}
	s.Close()
}
