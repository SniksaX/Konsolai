package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
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
	if act.Kind() != ReadOnly {
		t.Fatal("want ReadOnly")
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
