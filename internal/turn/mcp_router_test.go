package turn

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// TestMCPRouterLinesCarryNoSecretsOrSchemas: the router line is name and
// policy only — never command, args, env values, header values or URL.
func TestMCPRouterLinesCarryNoSecretsOrSchemas(t *testing.T) {
	secret := "sk-" + strings.Repeat("a", 24)
	profile := config.Builtin()
	server := config.DefaultMCPServer()
	server.Transport = "http"
	server.URL = "https://mcp.example.invalid/rpc?token=" + secret
	server.Headers = map[string]string{"Authorization": "Bearer " + secret}
	server.Env = map[string]string{"TOKEN": secret}
	server.Command = "server-binary"
	server.Args = []string{"--token", secret}
	server.Tools.Allow = []string{"read_*"}
	server.Tools.Deny = []string{"write_*"}
	disabled := config.DefaultMCPServer()
	disabled.Transport = "stdio"
	disabled.Command = "other-binary"
	disabled.Enabled = false
	disabled.Private = true
	profile.MCP.Servers = map[string]config.MCPServer{"github": server, "off": disabled}
	lines := mcpRouterLines(profile)
	if len(lines) != 2 {
		t.Fatalf("lines %v", lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"github: http, trust ask, enabled", "1 allow globs", "1 deny globs", "off: stdio, trust ask, disabled, private"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	for _, forbidden := range []string{secret, "mcp.example.invalid", "server-binary", "other-binary", "--token", "Bearer", "Authorization", "TOKEN"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("router line leaks %q:\n%s", forbidden, joined)
		}
	}
}
