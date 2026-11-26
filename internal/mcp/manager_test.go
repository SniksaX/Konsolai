package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func httpManager(t *testing.T, ts string, opts Options) *Manager {
	t.Helper()
	spec := Spec{Name: "fake", Transport: TransportHTTP, URL: ts, Enabled: true, Timeout: 10 * time.Second}
	m, err := NewManager([]Spec{spec}, opts)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	return m
}

func toolNames(tools []Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	return out
}

func hasTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestManagerHTTPConnectListAndCall(t *testing.T) {
	_, ts := newFakeHTTPServer(t)
	m := httpManager(t, ts.URL, Options{})

	ctx := context.Background()
	tools, err := m.Tools(ctx, "fake")
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if !hasTool(tools, "echo") || !hasTool(tools, "structured") {
		t.Fatalf("tools %v", toolNames(tools))
	}
	for _, tool := range tools {
		if tool.Name == "echo" && !tool.ReadOnly {
			t.Fatalf("echo lost its readOnlyHint: %+v", tool)
		}
		if tool.Name == "fails" && tool.ReadOnly {
			t.Fatalf("fails gained a readOnlyHint: %+v", tool)
		}
	}

	res, err := m.Call(ctx, "fake", "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError || len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, "hi") {
		t.Fatalf("echo result %+v", res)
	}

	structured, err := m.Call(ctx, "fake", "structured", nil)
	if err != nil {
		t.Fatalf("structured: %v", err)
	}
	var payload struct {
		Answer float64 `json:"answer"`
	}
	if err := json.Unmarshal(structured.Structured, &payload); err != nil || payload.Answer != 42 {
		t.Fatalf("structured %+v err %v", structured, err)
	}

	failed, err := m.Call(ctx, "fake", "fails", nil)
	if err != nil {
		t.Fatalf("tool error returned as call error: %v", err)
	}
	if !failed.IsError {
		t.Fatalf("tool error lost: %+v", failed)
	}
}

func TestManagerStdioConnectListAndCall(t *testing.T) {
	spec := stdioSpec(t, "basic", nil)
	m, err := NewManager([]Spec{spec}, Options{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })

	ctx := context.Background()
	tools, err := m.Tools(ctx, "fake")
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if !hasTool(tools, "echo") {
		t.Fatalf("tools %v", toolNames(tools))
	}
	res, err := m.Call(ctx, "fake", "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content[0].Text, "hi") {
		t.Fatalf("result %+v", res)
	}
}

func TestManagerValidatesSpecs(t *testing.T) {
	base := Spec{Name: "a", Transport: TransportStdio, Command: "/bin/echo", Enabled: true}
	cases := []struct {
		name  string
		specs []Spec
	}{
		{"duplicate", []Spec{base, base}},
		{"no name", []Spec{{Name: "", Transport: TransportStdio, Command: "/bin/echo"}}},
		{"unknown transport", []Spec{{Name: "a", Transport: "carrier-pigeon"}}},
		{"stdio without command", []Spec{{Name: "a", Transport: TransportStdio}}},
		{"stdio with url", []Spec{{Name: "a", Transport: TransportStdio, Command: "/bin/echo", URL: "http://localhost"}}},
		{"http without url", []Spec{{Name: "a", Transport: TransportHTTP}}},
		{"http with command", []Spec{{Name: "a", Transport: TransportHTTP, URL: "http://localhost", Command: "/bin/echo"}}},
		{"negative timeout", []Spec{{Name: "a", Transport: TransportHTTP, URL: "http://localhost", Timeout: -time.Second}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewManager(tc.specs, Options{}); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
}

func TestManagerUnknownAndDisabledServer(t *testing.T) {
	specs := []Spec{
		{Name: "off", Transport: TransportHTTP, URL: "http://127.0.0.1:1", Enabled: false},
	}
	m, err := NewManager(specs, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := m.Tools(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unknown server error %v", err)
	}
	if _, err := m.Tools(context.Background(), "off"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled server error %v", err)
	}
	if m.ServerEnabled("off") {
		t.Fatal("disabled server reported enabled")
	}
}

func TestManagerConcurrencyCap(t *testing.T) {
	srv, ts := newFakeHTTPServer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv.AddTool(fakeTool("gate", "wait for release", false), func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return textResult("released"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	m := httpManager(t, ts.URL, Options{MaxConcurrentCalls: 1})
	ctx := context.Background()
	if _, err := m.Tools(ctx, "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}

	first := make(chan error, 1)
	go func() {
		_, err := m.Call(ctx, "fake", "gate", nil)
		first <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the gate tool never ran")
	}

	secondCtx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	if _, err := m.Call(secondCtx, "fake", "gate", nil); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second call did not wait for the cap: %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestManagerCloseAllIsIdempotent(t *testing.T) {
	_, ts := newFakeHTTPServer(t)
	m := httpManager(t, ts.URL, Options{})
	if _, err := m.Tools(context.Background(), "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	if err := m.CloseAll(); err != nil {
		t.Fatalf("close all: %v", err)
	}
	if err := m.CloseAll(); err != nil {
		t.Fatalf("second close all: %v", err)
	}
	if _, err := m.Tools(context.Background(), "fake"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("tools after close: %v", err)
	}
}

func TestManagerToolsCached(t *testing.T) {
	var mu sync.Mutex
	lists := 0
	srv := newFakeServer("http")
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"tools/list"`) {
				mu.Lock()
				lists++
				mu.Unlock()
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
		}
		inner.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	m := httpManager(t, ts.URL, Options{})
	ctx := context.Background()
	if _, err := m.Tools(ctx, "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	if _, err := m.Tools(ctx, "fake"); err != nil {
		t.Fatalf("second tools: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if lists != 1 {
		t.Fatalf("tools/list requests %d, want one cached read", lists)
	}
}
