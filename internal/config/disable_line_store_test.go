package config

import (
	"errors"
	"strings"
	"testing"
)

func TestDisableLineDistinguishesAStoreError(t *testing.T) {
	got := DisableLine("postgres://u:secret@127.0.0.1:1/k", errors.New("store: append message: permission denied"))
	if strings.Contains(got, "secret") || strings.Contains(got, "\n") {
		t.Fatalf("leaked: %q", got)
	}
	if !strings.HasPrefix(got, "memory disabled after a store error: ") {
		t.Fatalf("want the store-error form, got %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Fatalf("cause missing: %q", got)
	}
}
