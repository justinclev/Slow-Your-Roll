//go:build integration

package redis_test

import (
	"context"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/justinclev/slow-your-roll/domain"
	rstore "github.com/justinclev/slow-your-roll/store/redis"
)

func newTestClient(t *testing.T) goredis.UniversalClient {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis unavailable at %s: %v", addr, err)
	}
	return client
}

func TestRedisStore_GetMiss(t *testing.T) {
	client := newTestClient(t)
	defer client.Close()
	s := rstore.New(client, "rl_test:")

	_, found, err := s.Get(context.Background(), domain.Key("missing"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found = false on miss")
	}
}

func TestRedisStore_SetAndGetHit(t *testing.T) {
	client := newTestClient(t)
	defer client.Close()
	s := rstore.New(client, "rl_test:")

	key := domain.Key("integration-set-get")
	defer client.Del(context.Background(), "rl_test:integration-set-get")

	entry := domain.Entry{
		Data:      []byte(`{"tokens":5}`),
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
	if string(got.Data) != string(entry.Data) {
		t.Errorf("Data = %q, want %q", got.Data, entry.Data)
	}
}

func TestRedisStore_Delete(t *testing.T) {
	client := newTestClient(t)
	defer client.Close()
	s := rstore.New(client, "rl_test:")

	key := domain.Key("integration-delete")
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

func TestRedisStore_DeleteIdempotent(t *testing.T) {
	client := newTestClient(t)
	defer client.Close()
	s := rstore.New(client, "rl_test:")

	if err := s.Delete(context.Background(), domain.Key("nonexistent")); err != nil {
		t.Fatalf("unexpected error on idempotent delete: %v", err)
	}
}
