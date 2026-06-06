package domain_test

import (
	"testing"
	"time"

	"github.com/justinclev/slow-your-roll/domain"
)

func TestNewPolicy_Valid(t *testing.T) {
	p, err := domain.NewPolicy(10, time.Minute, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Limit() != 10 {
		t.Errorf("Limit = %d, want 10", p.Limit())
	}
	if p.Window() != time.Minute {
		t.Errorf("Window = %v, want %v", p.Window(), time.Minute)
	}
	if p.Burst() != 10 {
		t.Errorf("Burst = %d, want 10", p.Burst())
	}
}

func TestNewPolicy_BurstGreaterThanLimit(t *testing.T) {
	p, err := domain.NewPolicy(10, time.Minute, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Burst() != 20 {
		t.Errorf("Burst = %d, want 20", p.Burst())
	}
}

func TestNewPolicy_InvalidLimit(t *testing.T) {
	_, err := domain.NewPolicy(0, time.Minute, 0)
	if err != domain.ErrInvalidLimit {
		t.Errorf("error = %v, want ErrInvalidLimit", err)
	}

	_, err = domain.NewPolicy(-1, time.Minute, 0)
	if err != domain.ErrInvalidLimit {
		t.Errorf("error = %v, want ErrInvalidLimit", err)
	}
}

func TestNewPolicy_InvalidWindow(t *testing.T) {
	_, err := domain.NewPolicy(10, 0, 10)
	if err != domain.ErrInvalidWindow {
		t.Errorf("error = %v, want ErrInvalidWindow", err)
	}

	_, err = domain.NewPolicy(10, -time.Second, 10)
	if err != domain.ErrInvalidWindow {
		t.Errorf("error = %v, want ErrInvalidWindow", err)
	}
}

func TestNewPolicy_InvalidBurst(t *testing.T) {
	_, err := domain.NewPolicy(10, time.Minute, 9)
	if err != domain.ErrInvalidBurst {
		t.Errorf("error = %v, want ErrInvalidBurst", err)
	}
}

func TestPolicy_WithLimit(t *testing.T) {
	p, _ := domain.NewPolicy(10, time.Minute, 20)
	p2, err := p.WithLimit(15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.Limit() != 15 {
		t.Errorf("Limit = %d, want 15", p2.Limit())
	}
	// original unchanged
	if p.Limit() != 10 {
		t.Errorf("original Limit = %d, want 10", p.Limit())
	}
}

func TestPolicy_WithLimit_BurstViolation(t *testing.T) {
	p, _ := domain.NewPolicy(10, time.Minute, 10)
	_, err := p.WithLimit(15)
	if err != domain.ErrInvalidBurst {
		t.Errorf("error = %v, want ErrInvalidBurst", err)
	}
}

func TestPolicy_WithWindow(t *testing.T) {
	p, _ := domain.NewPolicy(10, time.Minute, 10)
	p2, err := p.WithWindow(time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.Window() != time.Hour {
		t.Errorf("Window = %v, want %v", p2.Window(), time.Hour)
	}
	if p.Window() != time.Minute {
		t.Errorf("original Window = %v, want %v", p.Window(), time.Minute)
	}
}

func TestPolicy_WithWindow_InvalidWindow(t *testing.T) {
	p, _ := domain.NewPolicy(10, time.Minute, 10)
	_, err := p.WithWindow(-time.Second)
	if err != domain.ErrInvalidWindow {
		t.Errorf("error = %v, want ErrInvalidWindow", err)
	}
}

func TestPolicy_WithBurst(t *testing.T) {
	p, _ := domain.NewPolicy(10, time.Minute, 10)
	p2, err := p.WithBurst(20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.Burst() != 20 {
		t.Errorf("Burst = %d, want 20", p2.Burst())
	}
	if p.Burst() != 10 {
		t.Errorf("original Burst = %d, want 10", p.Burst())
	}
}

func TestPolicy_WithBurst_BelowLimit(t *testing.T) {
	p, _ := domain.NewPolicy(10, time.Minute, 10)
	_, err := p.WithBurst(5)
	if err != domain.ErrInvalidBurst {
		t.Errorf("error = %v, want ErrInvalidBurst", err)
	}
}
