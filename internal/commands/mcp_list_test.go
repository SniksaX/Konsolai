package commands

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// fakeMCPManager reports canned rows for /mcp list.
type fakeMCPManager map[string]MCPServerStatus

func (m fakeMCPManager) Status(name string) MCPServerStatus {
	if row, ok := m[name]; ok {
		return row
	}
	return MCPServerStatus{}
}

func mcpListEnv(t *testing.T) (*Env, *bytes.Buffer) {
	t.Helper()
	env, out, _ := testEnv(t)
	c := config.Builtin()
	fs := config.DefaultMCPServer()
	fs.Transport = config.MCPTransportStdio
	fs.Command = "npx"
	web := config.DefaultMCPServer()
	web.Transport = config.MCPTransportHTTP
	web.URL = "https://example.com/mcp"
	web.Enabled = false
	web.Trust = config.MCPTrustAuto
	web.Private = true
	c.MCP.Servers = map[string]config.MCPServer{"fs": fs, "web": web}
	env.Profile = c
	env.Out = out
	return env, out
}

func TestMCPListRows(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpListEnv(t)
	env.MCP = fakeMCPManager{
		"fs": {State: "connected", Tools: 4, ToolsKnown: true},
	}
	if code := runMCP(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	fs := lineWith(text, "fs")
	if !strings.Contains(fs, "stdio") || !strings.Contains(fs, "enabled") || !strings.Contains(fs, "ask") {
		t.Fatalf("fs row %q", fs)
	}
	if !strings.Contains(fs, "4") || !strings.Contains(fs, "connected") {
		t.Fatalf("fs manager row %q", fs)
	}
	web := lineWith(text, "web")
	if !strings.Contains(web, "http") || !strings.Contains(web, "disabled") || !strings.Contains(web, "auto") {
		t.Fatalf("web row %q", web)
	}
	if !strings.Contains(web, "private") || !strings.Contains(web, "not connected") {
		t.Fatalf("web row %q", web)
	}
	if !strings.Contains(web, "-") {
		t.Fatalf("web row lacks an unknown tool count: %q", web)
	}
}

func TestMCPListWithoutManager(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpListEnv(t)
	if code := runMCP(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "not connected") {
		t.Fatalf("no-manager list %q", out.String())
	}
	if strings.Contains(out.String(), "npx") {
		t.Fatal("/mcp list printed a command line")
	}
}

func TestMCPListEmpty(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out, _ := testEnv(t)
	env.Profile = config.Builtin()
	if code := runMCP(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "no MCP servers") {
		t.Fatalf("empty list %q", out.String())
	}
}

func TestMCPListMasksNoValues(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out, _ := testEnv(t)
	c := config.Builtin()
	fs := config.DefaultMCPServer()
	fs.Transport = config.MCPTransportStdio
	fs.Command = "npx"
	fs.Env = map[string]string{"API_KEY": "${MCP_KEY}"}
	fs.Headers = map[string]string{"Authorization": "Bearer ${MCP_KEY}"}
	fs.Private = true
	fs.Trust = config.MCPTrustReadOnly
	c.MCP.Servers = map[string]config.MCPServer{"fs": fs}
	env.Profile = c
	env.Confirm = fakeConf{d: ui.Approve}
	if code := runMCP(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String(), "${MCP_KEY}") || strings.Contains(out.String(), "Bearer") {
		t.Fatalf("the list printed env or header values: %q", out.String())
	}
}

func TestMCPCommandTree(t *testing.T) {
	env, _, _ := testEnv(t)
	cmd := Default().Lookup("mcp")
	if cmd == nil {
		t.Fatal("/mcp is not registered")
	}
	if cmd.Tree == nil {
		t.Fatal("/mcp has no tree")
	}
	level := cmd.Tree(env)
	if len(level) == 0 || level[0].Value != "list" {
		t.Fatalf("tree %+v", level)
	}
	if got := formatReadmeArgs(*cmd); got == "" {
		t.Fatal("/mcp renders no args")
	}
}

func lineWith(text, want string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

func TestMCPUnknownActionIsUsage(t *testing.T) {
	env, _, _ := testEnv(t)
	env.Profile = config.Builtin()
	if code := runMCP(context.Background(), env, []string{"tools", "fs"}); code != 2 {
		t.Fatalf("unknown /mcp action exit %d", code)
	}
	if code := runMCP(context.Background(), env, []string{"list", "extra"}); code != 2 {
		t.Fatalf("/mcp list extra exit %d", code)
	}
}
