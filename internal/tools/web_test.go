package tools

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/ui"
)

type memCache struct {
	title, text string
	puts        int
}

func (m *memCache) Get(context.Context, string) (string, string, bool) {
	if m.text == "" && m.title == "" {
		return "", "", false
	}
	return m.title, m.text, true
}

func (m *memCache) Put(_ context.Context, _, title, text string, _ time.Duration) error {
	m.title, m.text = title, text
	m.puts++
	return nil
}

func TestWebFetchExtractsAndCaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Hello</title></head><body><p>World</p></body></html>`))
	}))
	defer srv.Close()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := &memCache{}
	reg := NewRegistry(g, nil, nil, cache, nil)
	tool, ok := reg.Get("web_fetch")
	if !ok {
		t.Fatal("missing web_fetch")
	}
	act, err := tool.Prepare(`{"url":"` + srv.URL + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != Mutating {
		t.Fatal("httptest loopback must be Mutating")
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Output, "Hello") || !strings.Contains(res.Output, "World") {
		t.Fatalf("output %q", res.Output)
	}
	if strings.Contains(res.Meta, "cached") {
		t.Fatalf("first fetch cached: %q", res.Meta)
	}
	if cache.puts != 1 {
		t.Fatalf("puts %d", cache.puts)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatal(res.Output, err)
	}
	if !strings.Contains(res.Meta, "cached") {
		t.Fatalf("want cached meta, got %q", res.Meta)
	}
}

func TestWebFetchRejectsNonHTTP(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("web_fetch")
	if _, err := tool.Prepare(`{"url":"file:///etc/passwd"}`); err == nil {
		t.Fatal("want error")
	}
}

func TestWebFetchSpinner(t *testing.T) {
	var buf bytes.Buffer
	restore := ui.TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("web_fetch")
	act, err := tool.Prepare(`{"url":"` + srv.URL + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "fetching") || !strings.Contains(buf.String(), "\033[?25l") {
		t.Fatalf("spinner %q", buf.String())
	}
}

func prepareFetch(t *testing.T, rawURL string) Action {
	t.Helper()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("web_fetch")
	act, err := tool.Prepare(`{"url":"` + rawURL + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	return act
}

func TestWebFetchKindPrivateAlwaysMutating(t *testing.T) {
	orig := lookupIPs
	t.Cleanup(func() { lookupIPs = orig })
	cases := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.1"),
		net.ParseIP("192.168.1.1"),
		net.ParseIP("172.16.0.1"),
		net.ParseIP("169.254.1.1"),
		net.ParseIP("fc00::1"),
		net.ParseIP("fe80::1"),
	}
	for _, ip := range cases {
		lookupIPs = func(context.Context, string) ([]net.IP, error) {
			return []net.IP{ip}, nil
		}
		act := prepareFetch(t, "https://example.com/")
		if act.Kind() != Mutating {
			t.Fatalf("%s want Mutating got %v", ip, act.Kind())
		}
	}
}

func TestWebFetchKindAllowlistReadOnly(t *testing.T) {
	orig := lookupIPs
	lookupIPs = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.2.3.4")}, nil
	}
	t.Cleanup(func() { lookupIPs = orig })
	act := prepareFetch(t, "https://en.wikipedia.org/wiki/RAG")
	if act.Kind() != ReadOnly {
		t.Fatalf("allowlisted public want ReadOnly, got %v", act.Kind())
	}
}

func TestWebFetchKindNotAllowlistedMutating(t *testing.T) {
	orig := lookupIPs
	lookupIPs = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("1.2.3.4")}, nil
	}
	t.Cleanup(func() { lookupIPs = orig })
	act := prepareFetch(t, "https://evil.example/")
	if act.Kind() != Mutating {
		t.Fatalf("not allowlisted want Mutating, got %v", act.Kind())
	}
}
