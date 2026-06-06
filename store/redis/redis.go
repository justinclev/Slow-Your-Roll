package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/justinclev/slow-your-roll/domain"
)

// redisEntry is the JSON envelope stored in Redis.
// It wraps the opaque algorithm bytes with expiry metadata.
type redisEntry struct {
	Data      []byte    `json:"data"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store is a Redis-backed implementation of domain.Store and domain.Transactional.
// It accepts any redis.UniversalClient so callers can provide standalone, sentinel,
// or cluster clients without library changes. It is safe for concurrent use.
//
// Transact uses WATCH + MULTI/EXEC (optimistic locking) to provide atomic
// read-modify-write. Under high contention it retries automatically.
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

// Transact implements domain.Transactional using WATCH + MULTI/EXEC.
// It reads the current entry, calls fn, and writes the result atomically.
// If a concurrent writer modifies the key between the read and write,
// the transaction is retried automatically (optimistic locking).
func (s *Store) Transact(ctx context.Context, key domain.Key, fn func(domain.Entry, bool) (domain.Entry, error)) error {
	rkey := s.rkey(key)
	for {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			raw, getErr := tx.Get(ctx, rkey).Bytes()
			var entry domain.Entry
			var found bool
			if getErr == nil {
				var re redisEntry
				if jsonErr := json.Unmarshal(raw, &re); jsonErr != nil {
					return jsonErr
				}
				entry = domain.Entry{Data: re.Data, ExpiresAt: re.ExpiresAt}
				found = true
			} else if !errors.Is(getErr, redis.Nil) {
				return getErr
			}

			newEntry, fnErr := fn(entry, found)
			if fnErr != nil {
				return fnErr
			}

			re := redisEntry{Data: newEntry.Data, ExpiresAt: newEntry.ExpiresAt}
			raw, marshalErr := json.Marshal(re)
			if marshalErr != nil {
				return marshalErr
			}
			ttl := time.Until(newEntry.ExpiresAt)
			if ttl <= 0 {
				ttl = time.Second
			}

			_, pipeErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				return pipe.Set(ctx, rkey, raw, ttl).Err()
			})
			return pipeErr
		}, rkey)

		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return err
	}
}
