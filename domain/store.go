package domain

import (
	"context"
	"time"
)

// Entry is the opaque counter state persisted per key.
// Algorithms define their own concrete state; the store serialises it.
type Entry struct {
	Data      []byte
	ExpiresAt time.Time
}

// Store is the persistence port. Implementations must be safe for
// concurrent use by multiple goroutines.
type Store interface {
	// Get retrieves an entry. Returns (zero Entry, false, nil) when missing.
	Get(ctx context.Context, key Key) (Entry, bool, error)
	// Set upserts an entry with the given expiry.
	Set(ctx context.Context, key Key, entry Entry) error
	// Delete removes an entry. Idempotent.
	Delete(ctx context.Context, key Key) error
}

// Transactional extends Store with atomic read-modify-write.
// Algorithms use it to eliminate the TOCTOU race between Get and Set.
// Stores that implement this guarantee fn executes atomically with respect
// to concurrent calls for the same key.
type Transactional interface {
	Store
	// Transact reads the current Entry for key (zero value + false when absent),
	// calls fn, and if fn returns nil writes the returned Entry.
	Transact(ctx context.Context, key Key, fn func(Entry, bool) (Entry, error)) error
}
