package rag

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/store"
)

func TestFlushDropsPendingWhenTheStoreIsUnavailable(t *testing.T) {
	ing := New(nil, nil, "p")
	ing.items <- Item{Source: "turn", Content: "x"}
	if n := ing.Flush(context.Background(), time.Millisecond); n != 1 {
		t.Fatalf("nil store dropped %d want 1", n)
	}
	u := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if u == "" {
		return
	}
	st, err := store.Open(context.Background(), u)
	if err != nil {
		t.Skipf("test database: %v", err)
	}
	st.Close()
	ing = New(st, nil, "p")
	ing.items <- Item{Source: "turn", Content: "y"}
	if n := ing.Flush(context.Background(), time.Millisecond); n != 1 {
		t.Fatalf("closed store dropped %d want 1", n)
	}
}
