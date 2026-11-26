package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// mcpStatusEnv builds /status rows for one connected stdio server and one down
// private http server with an unresolved env reference.
func mcpStatusEnv(t *testing.T) (*Env, *strings.Builder) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	env, _, _ := testEnv(t)
	var out strings.Builder
	env.Out = &out
	c := config.Builtin()
	files := config.DefaultMCPServer()
	files.Transport = config.MCPTransportStdio
	files.Command = "npx"
	web := config.DefaultMCPServer()
	web.Transport = config.MCPTransportHTTP
	web.URL = "https://example.com/mcp"
	web.Trust = config.MCPTrustAuto
	web.Private = true
	web.Env = map[string]string{"DOCS_KEY": "${MCP_STATUS_TOKEN}"}
	c.MCP.Servers = map[string]config.MCPServer{"files": files, "web": web}
	env.Profile = c
	env.MCP = fakeMCPManager{
		"files": {State: "connected", Tools: 3, ToolsKnown: true, ToolNames: []string{"write_file", "read_file"}},
		"web":   {State: "down", Err: "connect failed", MissingEnv: []string{"MCP_STATUS_TOKEN"}},
	}
	return env, &out
}

func TestStatusMCPRows(t *testing.T) {
	env, out := mcpStatusEnv(t)
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	files := lineWith(text, "mcp            files")
	for _, want := range []string{"connected", "stdio", "trust ask", "3 tools"} {
		if !strings.Contains(files, want) {
			t.Fatalf("files row misses %q: %q", want, files)
		}
	}
	if !strings.Contains(text, "files: read_file") || !strings.Contains(text, "files: write_file") {
		t.Fatalf("tool lines are not server: tool:\n%s", text)
	}
	web := lineWith(text, "mcp            web")
	for _, want := range []string{"down", "http", "trust auto", "private", "missing MCP_STATUS_TOKEN", "connect failed"} {
		if !strings.Contains(web, want) {
			t.Fatalf("web row misses %q: %q", want, web)
		}
	}
	if strings.Contains(text, "${") || strings.Contains(text, "DOCS_KEY=") {
		t.Fatalf("/status printed an env value or reference:\n%s", text)
	}
	if strings.Contains(text, "npx") {
		t.Fatalf("/status printed a command line:\n%s", text)
	}
}

func TestStatusMCPNotConfigured(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out, _ := testEnv(t)
	env.Profile = config.Builtin()
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "mcp            not configured") {
		t.Fatalf("/status misses the not-configured MCP line:\n%s", out.String())
	}
}
