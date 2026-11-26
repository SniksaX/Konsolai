package repl

import (
	"testing"

	"github.com/SniksaX/Konsolai/internal/banner"
	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
)

// fakeMCPKnown implements the cheap manager surface the welcome reads.
type fakeMCPKnown map[string]string

func (f fakeMCPKnown) Status(string) commands.MCPServerStatus { return commands.MCPServerStatus{} }
func (f fakeMCPKnown) Known(name string) string               { return f[name] }

func mcpWelcomeEnv(t *testing.T) *commands.Env {
	t.Helper()
	c := config.Builtin()
	files := config.DefaultMCPServer()
	files.Transport = config.MCPTransportStdio
	files.Command = "npx"
	web := config.DefaultMCPServer()
	web.Transport = config.MCPTransportHTTP
	web.URL = "https://example.com/mcp"
	c.MCP.Servers = map[string]config.MCPServer{"files": files, "web": web}
	return &commands.Env{Profile: c, Cwd: t.TempDir()}
}

func TestMCPWelcomeCount(t *testing.T) {
	env := mcpWelcomeEnv(t)
	hook := mcpWelcomeCount(env)
	if hook == nil {
		t.Fatal("no MCP hook for a configured profile")
	}
	if up, total := hook(); up != 0 || total != 2 {
		t.Fatalf("without a manager: %d/%d", up, total)
	}
	env.MCP = fakeMCPKnown{"files": "connected"}
	if up, total := hook(); up != 1 || total != 2 {
		t.Fatalf("one connected: %d/%d", up, total)
	}
	env.MCP = fakeMCPKnown{"files": "connected", "web": "down"}
	if up, total := hook(); up != 1 || total != 2 {
		t.Fatalf("one down: %d/%d", up, total)
	}
}

func TestMCPWelcomeCountHidesWithoutServers(t *testing.T) {
	env := &commands.Env{Profile: config.Builtin(), Cwd: t.TempDir()}
	if up, total := mcpWelcomeCount(env)(); up != 0 || total != 0 {
		t.Fatalf("empty profile: %d/%d", up, total)
	}
	if mcpWelcomeCount(nil) != nil {
		t.Fatal("a nil env produced a hook")
	}
}

func TestWelcomeInputWiresMCP(t *testing.T) {
	env := mcpWelcomeEnv(t)
	env.MCP = fakeMCPKnown{"files": "connected"}
	in := welcomeInput(env, banner.Info{})
	if in.MCP == nil {
		t.Fatal("welcome input has no MCP hook")
	}
	if up, total := in.MCP(); up != 1 || total != 2 {
		t.Fatalf("welcome input MCP: %d/%d", up, total)
	}
}
