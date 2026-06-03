package transport

import (
	"testing"
	"time"

	"github.com/kkweon/csair/internal/auth"
)

func TestNewPacingDefault(t *testing.T) {
	c, err := New(auth.Token{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.minGap != 600*time.Millisecond || c.jitter != 500*time.Millisecond {
		t.Errorf("default pacing = %v/%v, want 600ms/500ms", c.minGap, c.jitter)
	}
}

func TestWithPacing(t *testing.T) {
	c, err := New(auth.Token{}, WithPacing(2*time.Second, time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.minGap != 2*time.Second || c.jitter != time.Second {
		t.Errorf("pacing = %v/%v, want 2s/1s", c.minGap, c.jitter)
	}
}
