// Package testerrors provides error-returning domain.Store stubs for algorithm tests.
package testerrors

import (
	"context"
	"errors"

	"github.com/your-org/ratelimiter/domain"
)

// ErrGet is returned by GetStore on Get.
var ErrGet = errors.New("store: get error")

// ErrSet is returned by SetStore on Set.
var ErrSet = errors.New("store: set error")

// GetStore always returns ErrGet from Get.
type GetStore struct{}

func (GetStore) Get(_ context.Context, _ domain.Key) (domain.Entry, bool, error) {
	return domain.Entry{}, false, ErrGet
}
func (GetStore) Set(_ context.Context, _ domain.Key, _ domain.Entry) error { return nil }
func (GetStore) Delete(_ context.Context, _ domain.Key) error              { return nil }

// SetStore succeeds on Get (returning nothing) but returns ErrSet from Set.
type SetStore struct{}

func (SetStore) Get(_ context.Context, _ domain.Key) (domain.Entry, bool, error) {
	return domain.Entry{}, false, nil
}
func (SetStore) Set(_ context.Context, _ domain.Key, _ domain.Entry) error {
	return ErrSet
}
func (SetStore) Delete(_ context.Context, _ domain.Key) error { return nil }

// CorruptStore returns a pre-set entry with bad JSON bytes on Get.
type CorruptStore struct{}

func (CorruptStore) Get(_ context.Context, _ domain.Key) (domain.Entry, bool, error) {
	return domain.Entry{Data: []byte("not-json")}, true, nil
}
func (CorruptStore) Set(_ context.Context, _ domain.Key, _ domain.Entry) error { return nil }
func (CorruptStore) Delete(_ context.Context, _ domain.Key) error              { return nil }
