package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/your-org/ratelimiter/domain"
)

// redisEntry is the JSON envelope stored in Redis.
// It wraps the opaque algorithm bytes with expiry metadata.
type redisEntry struct {
	Data      []byte    `json:"data"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store is a Redis-backed implementation of domain.Store.
// It accepts any redis.UniversalClient so callers can provide
// standalone, sentinel, or cluster clients without library changes.
// It is safe for concurrent use.
//
// Note: this store does NOT implement domain.Transactional.
// Algorithms fall back to a non-atomic Get+Set cycle, which is racy under
// concurrent writes from multiple processes. For distributed rate limiting,
// wrap algorithm logic in a Redis Lua script instead of using this store
// directly with concurrent writers.
type Store struct {
	client redis.UniversalClient
	prefix string
}

// New constructs a Store. prefix is prepended to every Redis key to
// namespace the library's data from other consumers of the same Redis.
// Panics if client is nil.
func New(client redis.UniversalClient, prefix string) *Store {
	if client == nil {
		panic("ratelimiter/redis: client must not be nil")
	}
	return &Store{client: client, prefix: prefix}
}

func (s *Store) rkey(k domain.Key) string {
	return s.prefix + string(k)
}

func (s *Store) Get(ctx context.Context, key domain.Key) (domain.Entry, bool, error) {
	raw, err := s.client.Get(ctx, s.rkey(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.Entry{}, false, nil
	}
	if err != nil {
		return domain.Entry{}, false, err
	}

	var re redisEntry
	if err := json.Unmarshal(raw, &re); err != nil {
		return domain.Entry{}, false, err
	}
	return domain.Entry{Data: re.Data, ExpiresAt: re.ExpiresAt}, true, nil
}

func (s *Store) Set(ctx context.Context, key domain.Key, entry domain.Entry) error {
	re := redisEntry{Data: entry.Data, ExpiresAt: entry.ExpiresAt}
	raw, err := json.Marshal(re)
	if err != nil {
		return err
	}
	ttl := time.Until(entry.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Second
	}
	return s.client.Set(ctx, s.rkey(key), raw, ttl).Err()
}

func (s *Store) Delete(ctx context.Context, key domain.Key) error {
	return s.client.Del(ctx, s.rkey(key)).Err()
}
