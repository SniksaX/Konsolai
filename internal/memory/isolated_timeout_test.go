package memory

import (
	"context"
	"testing"
	"time"
)

func TestGuardIsolatedTimeoutOverride(t *testing.T) {
	g := NewGuard(nil, nil)
	g.SetIsolatedTimeout(250 * time.Millisecond)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	iso, stop := g.Isolated(parent)
	defer stop()
	if err := iso.Err(); err != nil {
		t.Fatal(err)
	}
	dl, ok := iso.Deadline()
	if !ok {
		t.Fatal("missing deadline")
	}
	left := time.Until(dl)
	if left < 100*time.Millisecond || left > 250*time.Millisecond {
		t.Fatalf("deadline in %s", left)
	}
}
