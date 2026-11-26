package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// mcpCountEnv builds a profile with the given number of enabled stdio servers.
func mcpCountEnv(t *testing.T, names ...string) (*Env, *strings.Builder) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	env, _, _ := testEnv(t)
	var out strings.Builder
	env.Out = &out
	c := config.Builtin()
	c.MCP.Servers = map[string]config.MCPServer{}
	for _, name := range names {
		server := config.DefaultMCPServer()
		server.Transport = config.MCPTransportStdio
		server.Command = "npx"
		c.MCP.Servers[name] = server
	}
	env.Profile = c
	return env, &out
}

// TestStatusMCPCountConfiguredUntilConnected: a lazy server that has never
// connected reads "N configured", and the same profile reads "N/M up" on the
// next status after a connection.
func TestStatusMCPCountConfiguredUntilConnected(t *testing.T) {
	env, out := mcpCountEnv(t, "files", "web")
	env.MCP = fakeMCPManager{}
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	before := out.String()
	if !strings.Contains(before, "2 configured") {
		t.Fatalf("/status misses the configured count:\n%s", before)
	}
	if strings.Contains(before, "0/2 up") {
		t.Fatalf("/status painted the lazy state as a failure count:\n%s", before)
	}

	// The server connected: the next status flips the text.
	env.MCP = fakeMCPManager{"files": {State: "connected"}}
	out.Reset()
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	after := out.String()
	if !strings.Contains(after, "1/2 up") || strings.Contains(after, "2 configured") {
		t.Fatalf("/status did not flip the count after a connect:\n%s", after)
	}
}

// TestStatusMCPCountMultipleServers keeps the configured denominator and the
// connected numerator over several servers, including down and disabled ones.
func TestStatusMCPCountMultipleServers(t *testing.T) {
	env, out := mcpCountEnv(t, "files", "web", "docs")
	env.MCP = fakeMCPManager{
		"files": {State: "connected"},
		"web":   {State: "down"},
		"docs":  {State: "disabled"},
	}
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "1/3 up") {
		t.Fatalf("/status count over three servers:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "files: ") && !strings.Contains(out.String(), "mcp            files") {
		t.Fatalf("/status dropped the per-server rows:\n%s", out.String())
	}
}

// TestStatusMCPCountHidesWithoutServers keeps the count out of a profile with
// no configured server.
func TestStatusMCPCountHidesWithoutServers(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out, _ := testEnv(t)
	env.Profile = config.Builtin()
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "not configured") {
		t.Fatalf("/status misses the not-configured MCP line:\n%s", out.String())
	}
	if strings.Contains(out.String(), "0 configured") || strings.Contains(out.String(), "up\n") {
		t.Fatalf("/status painted a count with no server configured:\n%s", out.String())
	}
}
