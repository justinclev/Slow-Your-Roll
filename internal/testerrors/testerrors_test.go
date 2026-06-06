package testerrors_test

import (
	"context"
	"testing"

	"github.com/your-org/ratelimiter/domain"
	"github.com/your-org/ratelimiter/internal/testerrors"
)

func TestGetStore_ReturnsError(t *testing.T) {
	var s testerrors.GetStore
	_, _, err := s.Get(context.Background(), domain.Key("k"))
	if err != testerrors.ErrGet {
		t.Errorf("error = %v, want ErrGet", err)
	}
}

func TestGetStore_SetAndDeleteSucceed(t *testing.T) {
	var s testerrors.GetStore
	if err := s.Set(context.Background(), domain.Key("k"), domain.Entry{}); err != nil {
		t.Errorf("Set error = %v, want nil", err)
	}
	if err := s.Delete(context.Background(), domain.Key("k")); err != nil {
		t.Errorf("Delete error = %v, want nil", err)
	}
}

func TestSetStore_GetSucceeds(t *testing.T) {
	var s testerrors.SetStore
	_, found, err := s.Get(context.Background(), domain.Key("k"))
	if err != nil {
		t.Errorf("Get error = %v, want nil", err)
	}
	if found {
		t.Error("expected found = false")
	}
}

func TestSetStore_ReturnsError(t *testing.T) {
	var s testerrors.SetStore
	err := s.Set(context.Background(), domain.Key("k"), domain.Entry{})
	if err != testerrors.ErrSet {
		t.Errorf("error = %v, want ErrSet", err)
	}
}

func TestSetStore_DeleteSucceeds(t *testing.T) {
	var s testerrors.SetStore
	if err := s.Delete(context.Background(), domain.Key("k")); err != nil {
		t.Errorf("Delete error = %v, want nil", err)
	}
}

func TestCorruptStore_GetReturnsCorruptData(t *testing.T) {
	var s testerrors.CorruptStore
	entry, found, err := s.Get(context.Background(), domain.Key("k"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found = true")
	}
	if string(entry.Data) != "not-json" {
		t.Errorf("Data = %q, want %q", entry.Data, "not-json")
	}
}

func TestCorruptStore_SetAndDeleteSucceed(t *testing.T) {
	var s testerrors.CorruptStore
	if err := s.Set(context.Background(), domain.Key("k"), domain.Entry{}); err != nil {
		t.Errorf("Set error = %v, want nil", err)
	}
	if err := s.Delete(context.Background(), domain.Key("k")); err != nil {
		t.Errorf("Delete error = %v, want nil", err)
	}
}
