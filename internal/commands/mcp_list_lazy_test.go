package commands

import (
	"context"
	"strings"
	"testing"
)

// lazyMCPManager is a manager with the lazy capability: Known and the cached
// count never connect, and Status records any call that would have connected.
type lazyMCPManager struct {
	known       map[string]string
	counts      map[string]int
	statusCalls int
}

func (m *lazyMCPManager) Status(name string) MCPServerStatus {
	m.statusCalls++
	return MCPServerStatus{State: "connected", Tools: 99, ToolsKnown: true}
}

func (m *lazyMCPManager) Known(name string) string {
	return m.known[name]
}

func (m *lazyMCPManager) CachedToolCount(name string) (int, bool) {
	n, ok := m.counts[name]
	return n, ok
}

// TestMCPListNeverConnects proves /mcp list reads only Known and the cached
// count: no Status call means no server is connected by the listing. It then
// shows the same profile flipping after a connection.
func TestMCPListNeverConnects(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpListEnv(t)
	manager := &lazyMCPManager{
		known:  map[string]string{"fs": "not connected", "web": "disabled"},
		counts: map[string]int{},
	}
	env.MCP = manager
	if code := runMCP(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if manager.statusCalls != 0 {
		t.Fatalf("/mcp list called Status %d times and forced a connect", manager.statusCalls)
	}
	text := out.String()
	fs := lineWith(text, "fs")
	if !strings.Contains(fs, "not connected") || !strings.Contains(fs, "not") {
		t.Fatalf("fs row before a connect %q", fs)
	}
	if !strings.Contains(fs, "-") {
		t.Fatalf("fs row invented a tool count: %q", fs)
	}

	// A connection happened earlier: state and the cached count appear on the
	// next list, still without a Status call.
	manager.known["fs"] = "connected"
	manager.counts["fs"] = 4
	out.Reset()
	if code := runMCP(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if manager.statusCalls != 0 {
		t.Fatalf("the second list forced a connect: %d Status calls", manager.statusCalls)
	}
	fs = lineWith(out.String(), "fs")
	if !strings.Contains(fs, "connected") || !strings.Contains(fs, "4") {
		t.Fatalf("fs row after a cached connect %q", fs)
	}
}
