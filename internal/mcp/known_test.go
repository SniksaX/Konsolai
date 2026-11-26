package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestKnownNeverConnects proves the cheap accessor sends no request: the
// counter stays at zero until a real Tools call connects.
func TestKnownNeverConnects(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := newFakeServer("http")
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		inner.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	m := httpManager(t, ts.URL, Options{})
	if got := m.Known("fake"); got != "not connected" {
		t.Fatalf("before any call Known = %q, want not connected", got)
	}
	mu.Lock()
	before := requests
	mu.Unlock()
	if before != 0 {
		t.Fatalf("Known sent %d requests", before)
	}
	if _, err := m.Tools(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	if got := m.Known("fake"); got != "connected" {
		t.Fatalf("after Tools Known = %q, want connected", got)
	}
	mu.Lock()
	after := requests
	mu.Unlock()
	if after == before {
		t.Fatal("the real connection made no request")
	}
}

func TestKnownDisabledAndUnknown(t *testing.T) {
	m, err := NewManager([]Spec{{
		Name: "off", Transport: TransportStdio, Command: "anything", Enabled: false,
	}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Known("off"); got != "disabled" {
		t.Fatalf("disabled Known = %q", got)
	}
	if got := m.Known("missing"); got != "not connected" {
		t.Fatalf("unknown Known = %q", got)
	}
	var nilM *Manager
	if got := nilM.Known("x"); got != "not connected" {
		t.Fatalf("nil Known = %q", got)
	}
}
