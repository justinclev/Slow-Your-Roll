package redis_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/justinclev/slow-your-roll/domain"
	rstore "github.com/justinclev/slow-your-roll/store/redis"
)

func newStore(t *testing.T) (*rstore.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	return rstore.New(client, "rl:"), mr
}

func TestRedisStore_Unit_GetMiss(t *testing.T) {
	s, _ := newStore(t)
	_, found, err := s.Get(context.Background(), domain.Key("missing"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found = false on miss")
	}
}

func TestRedisStore_Unit_SetAndGetHit(t *testing.T) {
	s, _ := newStore(t)
	key := domain.Key("mykey")
	data, _ := json.Marshal(map[string]int{"tokens": 5})
	entry := domain.Entry{
		Data:      data,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := s.Set(context.Background(), key, entry); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	got, found, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if !found {
		t.Fatal("expected found = true")
	}
	if string(got.Data) != string(data) {
		t.Errorf("Data = %q, want %q", got.Data, data)
	}
}

func TestRedisStore_Unit_Delete(t *testing.T) {
	s, _ := newStore(t)
	key := domain.Key("del-key")
	_ = s.Set(context.Background(), key, domain.Entry{
		Data:      []byte("x"),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err := s.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	_, found, _ := s.Get(context.Background(), key)
	if found {
		t.Error("expected found = false after Delete")
	}
}

func TestRedisStore_Unit_DeleteIdempotent(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Delete(context.Background(), domain.Key("nonexistent")); err != nil {
		t.Fatalf("unexpected error on idempotent delete: %v", err)
	}
}

func TestRedisStore_Unit_SetOverwrites(t *testing.T) {
	s, _ := newStore(t)
	key := domain.Key("overwrite")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte(`"first"`), ExpiresAt: time.Now().Add(time.Minute)})
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte(`"second"`), ExpiresAt: time.Now().Add(time.Minute)})

	got, found, _ := s.Get(context.Background(), key)
	if !found {
		t.Fatal("expected found = true")
	}
	if string(got.Data) != `"second"` {
		t.Errorf("Data = %q, want %q", got.Data, `"second"`)
	}
}

func TestRedisStore_Unit_NilClientPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil client")
		}
	}()
	rstore.New(nil, "rl:")
}

func TestRedisStore_Unit_SetExpiredTTLUsesOneSecond(t *testing.T) {
	s, mr := newStore(t)
	key := domain.Key("expired-ttl")
	// ExpiresAt in the past → TTL floored to 1s.
	_ = s.Set(context.Background(), key, domain.Entry{
		Data:      []byte("x"),
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	// Advance miniredis clock 2 seconds → entry should have expired.
	mr.FastForward(2 * time.Second)

	_, found, _ := s.Get(context.Background(), key)
	if found {
		t.Error("expected found = false after TTL expiry")
	}
}

func TestRedisStore_Transact_CreateOnMiss(t *testing.T) {
	s, _ := newStore(t)
	key := domain.Key("tx-create")

	err := s.Transact(context.Background(), key, func(entry domain.Entry, found bool) (domain.Entry, error) {
		if found {
			t.Error("expected found = false on first Transact")
		}
		return domain.Entry{Data: []byte("v1"), ExpiresAt: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatalf("Transact error: %v", err)
	}

	got, ok, _ := s.Get(context.Background(), key)
	if !ok {
		t.Fatal("expected entry after Transact")
	}
	if string(got.Data) != "v1" {
		t.Errorf("Data = %q, want %q", got.Data, "v1")
	}
}

func TestRedisStore_Transact_UpdateOnHit(t *testing.T) {
	s, _ := newStore(t)
	key := domain.Key("tx-update")

	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("v1"), ExpiresAt: time.Now().Add(time.Minute)})

	err := s.Transact(context.Background(), key, func(entry domain.Entry, found bool) (domain.Entry, error) {
		if !found {
			t.Error("expected found = true on second Transact")
		}
		if string(entry.Data) != "v1" {
			t.Errorf("entry.Data = %q, want %q", entry.Data, "v1")
		}
		return domain.Entry{Data: []byte("v2"), ExpiresAt: time.Now().Add(time.Minute)}, nil
	})
	if err != nil {
		t.Fatalf("Transact error: %v", err)
	}

	got, _, _ := s.Get(context.Background(), key)
	if string(got.Data) != "v2" {
		t.Errorf("Data = %q, want %q", got.Data, "v2")
	}
}

func TestRedisStore_Transact_FnErrorAbortsWrite(t *testing.T) {
	s, _ := newStore(t)
	key := domain.Key("tx-fn-err")
	_ = s.Set(context.Background(), key, domain.Entry{Data: []byte("original"), ExpiresAt: time.Now().Add(time.Minute)})

	fnErr := errors.New("fn failure")
	err := s.Transact(context.Background(), key, func(entry domain.Entry, found bool) (domain.Entry, error) {
		return domain.Entry{}, fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Errorf("error = %v, want %v", err, fnErr)
	}

	got, _, _ := s.Get(context.Background(), key)
	if string(got.Data) != "original" {
		t.Error("store must not be mutated when fn returns error")
	}
}

func TestRedisStore_ImplementsTransactional(t *testing.T) {
	s, _ := newStore(t)
	var _ interface {
		Transact(context.Context, domain.Key, func(domain.Entry, bool) (domain.Entry, error)) error
	} = s
}

func TestRedisStore_Unit_GetCorruptJSON(t *testing.T) {
	s, mr := newStore(t)
	mr.Set("rl:corrupt-key", "not-valid-json")

	_, _, err := s.Get(context.Background(), domain.Key("corrupt-key"))
	if err == nil {
		t.Error("expected error for corrupt JSON, got nil")
	}
}

func TestRedisStore_Transact_CorruptJSON(t *testing.T) {
	s, mr := newStore(t)
	mr.Set("rl:corrupt-tx", "not-valid-json")

	err := s.Transact(context.Background(), domain.Key("corrupt-tx"), func(entry domain.Entry, found bool) (domain.Entry, error) {
		t.Error("fn must not be called when JSON is corrupt")
		return domain.Entry{}, nil
	})
	if err == nil {
		t.Error("expected error for corrupt JSON in Transact, got nil")
	}
}
