package domain

import (
	"errors"
	"strings"
)

// Key identifies the subject being rate limited.
type Key string

var ErrEmptyKey = errors.New("key must not be empty")

// NewKey constructs a validated Key.
func NewKey(raw string) (Key, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrEmptyKey
	}
	return Key(raw), nil
}

// Compose joins segments into a namespaced key (e.g. "user:42:upload").
func Compose(segments ...string) Key {
	return Key(strings.Join(segments, ":"))
}
