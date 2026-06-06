package domain

import (
	"context"
	"time"
)

// Algorithm is the strategy port. Each counting algorithm implements this.
// Allow must be atomic with respect to the provided Store.
type Algorithm interface {
	Allow(ctx context.Context, key Key, policy Policy, store Store, now time.Time) (Result, error)
}
