package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// TestMCPToolsOfferedColumn shows what the model would be offered right now:
// trust read_only hides mutating tools, allow/deny globs hide by name, auto
// trust keeps both kinds, and the column follows a live trust change.
func TestMCPToolsOfferedColumn(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	env.MCP = mcpInspectInspector()

	offered := func(name string) string {
		line := lineWith(out.String(), name+" ")
		if line == "" {
			line = lineWith(out.String(), name)
		}
		return line
	}
	tools := func(server string) {
		t.Helper()
		out.Reset()
		if code := runMCP(context.Background(), env, []string{"tools", server}); code != 0 {
			t.Fatalf("%s tools exit %d: %s", server, code, out.String())
		}
	}

	// trust ask: the read-only and the mutating tool are both offered.
	tools("docs")
	head := lineWith(out.String(), "mcp docs:")
	if !strings.Contains(head, "2 offered now") {
		t.Fatalf("docs summary %q", head)
	}
	if read := offered("read_file"); !strings.Contains(read, "yes") {
		t.Fatalf("read_file not offered: %q", read)
	}
	if write := offered("write_file"); !strings.Contains(write, "yes") {
		t.Fatalf("write_file not offered under ask: %q", write)
	}

	// trust read_only hides the mutating tool and the reason says why.
	tools("locked")
	if list := offered("list_things"); !strings.Contains(list, "yes") {
		t.Fatalf("locked read-only tool not offered: %q", list)
	}
	del := offered("delete_thing")
	if strings.Contains(del, "yes") || !strings.Contains(del, "no: trust read_only") {
		t.Fatalf("locked mutating tool offer %q", del)
	}

	// allow and deny globs.
	tools("tight")
	if one := offered("read_one"); !strings.Contains(one, "yes") {
		t.Fatalf("read_one not offered: %q", one)
	}
	if secret := offered("secret_tool"); !strings.Contains(secret, "no: not in allow") {
		t.Fatalf("secret_tool offer %q", secret)
	}
	if danger := offered("danger"); !strings.Contains(danger, "no: deny") {
		t.Fatalf("danger offer %q", danger)
	}

	// auto keeps both kinds; the hint only changes the kind.
	tools("auto")
	if alpha := offered("alpha"); !strings.Contains(alpha, "yes") {
		t.Fatalf("alpha offer %q", alpha)
	}
	if beta := offered("beta"); !strings.Contains(beta, "yes") {
		t.Fatalf("beta offer %q", beta)
	}

	// Live trust change: the same command reflects the new profile at once.
	server, _ := env.Profile.MCPServer("locked")
	server.Trust = config.MCPTrustAsk
	env.Profile.MCP.Servers["locked"] = server
	tools("locked")
	if del := offered("delete_thing"); !strings.Contains(del, "yes") {
		t.Fatalf("after trust ask the mutating tool is still hidden: %q", del)
	}
	server.Trust = config.MCPTrustReadOnly
	env.Profile.MCP.Servers["locked"] = server
	tools("locked")
	if del := offered("delete_thing"); !strings.Contains(del, "no: trust read_only") {
		t.Fatalf("after trust read_only the mutating tool is offered: %q", del)
	}
}

// TestMCPToolsOfferedPrivateAndCap: a private server under a remote main model
// is not offered without a confirmation, and above the inline cap the direct
// tools are not offered either.
func TestMCPToolsOfferedPrivateAndCap(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	env.MCP = mcpInspectInspector()

	server, _ := env.Profile.MCPServer("docs")
	server.Private = true
	env.Profile.MCP.Servers["docs"] = server
	env.Profile.SetMain("http://203.0.113.9/v1", "remote-model", false)
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if read := lineWith(out.String(), "read_file"); !strings.Contains(read, "no: private (remote main)") {
		t.Fatalf("private server offer %q", read)
	}

	// With --yes the private server is offered (the session auto-approves it).
	env.Yes = true
	out.Reset()
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if read := lineWith(out.String(), "read_file"); !strings.Contains(read, "yes") {
		t.Fatalf("private server under --yes offer %q", read)
	}

	// Above the cap only mcp_find_tool is offered directly.
	env.Yes = false
	server.Private = false
	env.Profile.MCP.Servers["docs"] = server
	env.Profile.MCP.MaxInlineTools = 1
	out.Reset()
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if write := lineWith(out.String(), "write_file"); !strings.Contains(write, "no: find tool only") {
		t.Fatalf("capped tool offer %q", write)
	}
	head := lineWith(out.String(), "mcp docs:")
	if !strings.Contains(head, "0 offered now") {
		t.Fatalf("capped summary %q", head)
	}
}
