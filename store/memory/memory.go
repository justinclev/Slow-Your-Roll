package memory

import (
	"context"
	"sync"
	"time"

	"github.com/justinclev/slow-your-roll/domain"
)

type record struct {
	entry     domain.Entry
	expiresAt time.Time
}

// Store is a goroutine-safe in-memory implementation of domain.Store.
// It also implements domain.Transactional so algorithms can perform
// atomic read-modify-write without a TOCTOU race.
//
// Intended for single-process deployments and testing.
// Call Close when the store is no longer needed to stop background cleanup.
type Store struct {
	mu      sync.RWMutex
	records map[domain.Key]record
	clock   func() time.Time
	stopCh  chan struct{}
}

func New() *Store {
	s := &Store{
		records: make(map[domain.Key]record),
		clock:   time.Now,
		stopCh:  make(chan struct{}),
	}
	go s.runCleanup()
	return s
}

// WithClock overrides the time source. Call before the store is shared
// across goroutines (construction time only).
func (s *Store) WithClock(fn func() time.Time) { s.clock = fn }

// Close stops the background cleanup goroutine.
func (s *Store) Close() {
	close(s.stopCh)
}

func (s *Store) Get(ctx context.Context, key domain.Key) (domain.Entry, bool, error) {
	s.mu.RLock()
	r, ok := s.records[key]
	s.mu.RUnlock()
	if !ok || s.clock().After(r.expiresAt) {
		return domain.Entry{}, false, nil
	}
	return r.entry, true, nil
}

func (s *Store) Set(ctx context.Context, key domain.Key, entry domain.Entry) error {
	s.mu.Lock()
	s.records[key] = record{entry: entry, expiresAt: entry.ExpiresAt}
	s.mu.Unlock()
	return nil
}

func (s *Store) Delete(ctx context.Context, key domain.Key) error {
	s.mu.Lock()
	delete(s.records, key)
	s.mu.Unlock()
	return nil
}

// Transact atomically reads the entry for key, calls fn with the current
// value, and if fn returns nil writes the returned entry. The lock is held
// for the duration of fn, preventing interleaved reads or writes for the key.
func (s *Store) Transact(ctx context.Context, key domain.Key, fn func(domain.Entry, bool) (domain.Entry, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.records[key]
	var entry domain.Entry
	var found bool
	if ok && !s.clock().After(r.expiresAt) {
		entry = r.entry
		found = true
	}

	newEntry, err := fn(entry, found)
	if err != nil {
		return err
	}
	s.records[key] = record{entry: newEntry, expiresAt: newEntry.ExpiresAt}
	return nil
}

func (s *Store) runCleanup() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.evictExpired()
		case <-s.stopCh:
			return
		}
	}
}

func (s *Store) evictExpired() {
	now := s.clock()
	s.mu.Lock()
	for k, r := range s.records {
		if now.After(r.expiresAt) {
			delete(s.records, k)
		}
	}
	s.mu.Unlock()
}
