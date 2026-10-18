package store

import (
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestResolvedOpenUsesBuiltinAndOverride(t *testing.T) {
	q, m := resolvedOpen(OpenOptions{})
	if q != config.Builtin().StoreQueryTimeout() || m != config.Builtin().StoreMigrateTimeout() {
		t.Fatalf("builtin query %s migrate %s", q, m)
	}
	q, m = resolvedOpen(OpenOptions{
		QueryTimeout:   40 * time.Millisecond,
		MigrateTimeout: 80 * time.Millisecond,
	})
	if q != 40*time.Millisecond || m != 80*time.Millisecond {
		t.Fatalf("override query %s migrate %s", q, m)
	}
}
