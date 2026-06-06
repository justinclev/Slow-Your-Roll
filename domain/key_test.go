package domain_test

import (
	"testing"

	"github.com/your-org/ratelimiter/domain"
)

func TestNewKey_Valid(t *testing.T) {
	k, err := domain.NewKey("user-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(k) != "user-42" {
		t.Errorf("key = %q, want %q", k, "user-42")
	}
}

func TestNewKey_Empty(t *testing.T) {
	_, err := domain.NewKey("")
	if err != domain.ErrEmptyKey {
		t.Errorf("error = %v, want ErrEmptyKey", err)
	}
}

func TestNewKey_WhitespaceOnly(t *testing.T) {
	_, err := domain.NewKey("   ")
	if err != domain.ErrEmptyKey {
		t.Errorf("error = %v, want ErrEmptyKey", err)
	}

	_, err = domain.NewKey("\t\n")
	if err != domain.ErrEmptyKey {
		t.Errorf("error = %v, want ErrEmptyKey", err)
	}
}

func TestCompose(t *testing.T) {
	k := domain.Compose("user", "42", "upload")
	if string(k) != "user:42:upload" {
		t.Errorf("key = %q, want %q", k, "user:42:upload")
	}
}

func TestCompose_Single(t *testing.T) {
	k := domain.Compose("user")
	if string(k) != "user" {
		t.Errorf("key = %q, want %q", k, "user")
	}
}

func TestCompose_Two(t *testing.T) {
	k := domain.Compose("user", "42")
	if string(k) != "user:42" {
		t.Errorf("key = %q, want %q", k, "user:42")
	}
}
