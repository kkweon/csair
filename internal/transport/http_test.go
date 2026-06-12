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

func TestNewRequesterSelectsImpl(t *testing.T) {
	std, err := NewRequester(auth.Token{}, false)
	if err != nil {
		t.Fatalf("NewRequester(false): %v", err)
	}
	if _, ok := std.(*Client); !ok {
		t.Errorf("useTLS=false: got %T, want *Client", std)
	}

	tls, err := NewRequester(auth.Token{}, true)
	if err != nil {
		t.Fatalf("NewRequester(true): %v", err)
	}
	if _, ok := tls.(*tlsClient); !ok {
		t.Errorf("useTLS=true: got %T, want *tlsClient", tls)
	}
}

func TestNewRequesterPropagatesPacing(t *testing.T) {
	r, err := NewRequester(auth.Token{}, true, WithPacing(3*time.Second, 2*time.Second))
	if err != nil {
		t.Fatalf("NewRequester: %v", err)
	}
	tc := r.(*tlsClient)
	if tc.minGap != 3*time.Second || tc.jitter != 2*time.Second {
		t.Errorf("tls pacing = %v/%v, want 3s/2s", tc.minGap, tc.jitter)
	}
}
