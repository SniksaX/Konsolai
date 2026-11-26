package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func serverPID(t *testing.T, m *Manager, name string) int {
	t.Helper()
	st, ok := m.servers[name]
	if !ok {
		t.Fatalf("no server %q", name)
	}
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	return st.pid
}

func serverRestarted(t *testing.T, m *Manager, name string) bool {
	t.Helper()
	st, ok := m.servers[name]
	if !ok {
		t.Fatalf("no server %q", name)
	}
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	return st.restarted
}

func TestStdioCrashRestartsOnceThenDown(t *testing.T) {
	state := t.TempDir() + "/crashes"
	spec := stdioSpec(t, "basic", map[string]string{"KONSOLAI_MCP_FAKE_STATE": state})
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	ctx := context.Background()

	if _, err := m.Tools(ctx, "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}

	res, err := m.Call(ctx, "fake", "crash-once", nil)
	if err != nil {
		t.Fatalf("a crash must restart once and retry: %v", err)
	}
	if len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, "survived") {
		t.Fatalf("retried result %+v", res)
	}
	if m.ServerDown("fake") {
		t.Fatal("server marked down after the one allowed restart")
	}
	if !serverRestarted(t, m, "fake") {
		t.Fatal("the crash was not recorded as a restart")
	}

	if _, err := m.Call(ctx, "fake", "crash-always", nil); err == nil || !errors.Is(err, ErrServerDown) {
		t.Fatalf("second crash error %v", err)
	}
	if !m.ServerDown("fake") {
		t.Fatal("server not marked down after the second crash")
	}
	if _, err := m.Tools(ctx, "fake"); err == nil || !errors.Is(err, ErrServerDown) {
		t.Fatalf("tools after down %v", err)
	}
}

func TestStdioCallTimeoutKillsTheGroupAndReconnects(t *testing.T) {
	spec := stdioSpec(t, "basic", nil)
	spec.Timeout = 150 * time.Millisecond
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	ctx := context.Background()

	if _, err := m.Tools(ctx, "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	pid := serverPID(t, m, "fake")
	if pid <= 0 {
		t.Fatalf("child pid %d", pid)
	}

	start := time.Now()
	if _, err := m.Call(ctx, "fake", "hang", nil); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("hanging call error %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the timeout took %s", elapsed)
	}
	processGone(t, pid)

	// A timeout is not a crash: the next use reconnects lazily.
	tools, err := m.Tools(ctx, "fake")
	if err != nil {
		t.Fatalf("reconnect after timeout: %v", err)
	}
	if !hasTool(tools, "echo") {
		t.Fatalf("tools after reconnect %v", toolNames(tools))
	}
	if m.ServerDown("fake") {
		t.Fatal("a timeout marked the server down")
	}
}

func TestStdioHangCloseAllKillsTheWholeGroup(t *testing.T) {
	pidsPath := t.TempDir() + "/pids"
	pw, lines := childStderr(t)
	spec := stdioSpec(t, "hang", map[string]string{"KONSOLAI_MCP_FAKE_PIDS": pidsPath})
	m, err := NewManager([]Spec{spec}, Options{Stderr: pw})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })

	if _, err := m.Tools(context.Background(), "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	waitLine(t, lines, "ready")
	child, grandchild, pgid := readPids(t, pidsPath)
	if child != serverPID(t, m, "fake") {
		t.Fatalf("pids file child %d, manager child %d", child, serverPID(t, m, "fake"))
	}
	if pgid != child {
		t.Fatalf("child pgid %d, want its own id %d", pgid, child)
	}

	if err := m.CloseAll(); err != nil {
		t.Fatalf("close all: %v", err)
	}
	processGone(t, child)
	// The grandchild is in the same group and was never signalled directly,
	// so its exit proves the whole group was killed.
	processGone(t, grandchild)
}

func TestManagerOversizedResult(t *testing.T) {
	_, ts := newFakeHTTPServer(t)
	m := httpManager(t, ts.URL, Options{MaxResultBytes: 64})
	ctx := context.Background()

	if _, err := m.Tools(ctx, "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	if _, err := m.Call(ctx, "fake", "big", nil); err == nil || !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("oversized result error %v", err)
	}
	// The server answered, so it is still healthy.
	res, err := m.Call(ctx, "fake", "echo", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("call after oversized result: %v", err)
	}
	if res.IsError {
		t.Fatalf("result %+v", res)
	}
	if m.ServerDown("fake") {
		t.Fatal("an oversized result marked the server down")
	}
}

func TestMalformedStdioOutputFailsToConnect(t *testing.T) {
	spec := stdioSpec(t, "malformed", nil)
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	if _, err := m.Tools(context.Background(), "fake"); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("malformed output error %v", err)
	}
}

func TestMalformedHTTPBodyFailsToConnect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`this is not json`))
	}))
	t.Cleanup(ts.Close)
	m := httpManager(t, ts.URL, Options{})
	if _, err := m.Tools(context.Background(), "fake"); err == nil {
		t.Fatal("a malformed HTTP body connected")
	}
}

func TestHTTPCallTimeout(t *testing.T) {
	srv, ts := newFakeHTTPServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.AddTool(fakeTool("wait-forever", "block until the caller gives up", false), func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		select {
		case <-release:
			return textResult("late"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	spec := Spec{Name: "fake", Transport: TransportHTTP, URL: ts.URL, Enabled: true, Timeout: 100 * time.Millisecond}
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	if _, err := m.Tools(context.Background(), "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	start := time.Now()
	if _, err := m.Call(context.Background(), "fake", "wait-forever", nil); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the timeout took %s", elapsed)
	}
}

func TestToolListChangedRefreshesTheCache(t *testing.T) {
	srv, ts := newFakeHTTPServer(t)
	changed := make(chan string, 4)
	spec := Spec{Name: "fake", Transport: TransportHTTP, URL: ts.URL, Enabled: true, Timeout: 10 * time.Second}
	m, err := NewManager([]Spec{spec}, Options{OnToolsChanged: func(server string) { changed <- server }})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	ctx := context.Background()

	before, err := m.Tools(ctx, "fake")
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if hasTool(before, "extra") {
		t.Fatal("the extra tool existed before the change")
	}
	if _, err := m.Call(ctx, "fake", "add-extra", nil); err != nil {
		t.Fatalf("add-extra: %v", err)
	}
	select {
	case server := <-changed:
		if server != "fake" {
			t.Fatalf("changed server %q", server)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no tool list changed notification arrived")
	}
	after, err := m.Tools(ctx, "fake")
	if err != nil {
		t.Fatalf("tools after change: %v", err)
	}
	if !hasTool(after, "extra") {
		t.Fatalf("the refreshed list is missing extra: %v", toolNames(after))
	}
	_ = srv
}
