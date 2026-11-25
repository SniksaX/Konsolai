package repl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func probeBackgroundProfile(endpoint string) config.Profile {
	c := config.Builtin()
	c.SetMain(endpoint, "", false)
	return c
}

func TestProbeBackgroundAuthFailureIsNotDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	st := &session{}
	probeBackground(context.Background(), probeBackgroundProfile(srv.URL+"/v1"), nil, st)

	if st.down {
		t.Fatal("a 401 marked the main model down")
	}
	joined := strings.Join(st.warns, "\n")
	if !strings.Contains(joined, "auth failed") {
		t.Fatalf("warnings %q", joined)
	}
	if strings.Contains(joined, "unreachable") {
		t.Fatalf("a 401 read as unreachable: %q", joined)
	}
}

func TestProbeBackgroundHTTPStatusIsNotDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	st := &session{}
	probeBackground(context.Background(), probeBackgroundProfile(srv.URL+"/v1"), nil, st)

	if st.down {
		t.Fatal("an HTTP status marked the main model down")
	}
	joined := strings.Join(st.warns, "\n")
	if !strings.Contains(joined, "http 500") {
		t.Fatalf("warnings %q", joined)
	}
}

func TestProbeBackgroundUnreachableIsDown(t *testing.T) {
	st := &session{}
	probeBackground(context.Background(), probeBackgroundProfile("http://127.0.0.1:1/v1"), nil, st)

	if !st.down {
		t.Fatal("a refused connection did not mark the main model down")
	}
	if joined := strings.Join(st.warns, "\n"); !strings.Contains(joined, "unreachable") {
		t.Fatalf("warnings %q", joined)
	}
}
