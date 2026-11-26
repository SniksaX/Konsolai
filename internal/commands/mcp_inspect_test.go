package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
)

// fakeMCPInspector reports a canned catalog and probe for /mcp tools and
// /mcp test. It embeds fakeMCPManager so the list surfaces keep working.
type fakeMCPInspector struct {
	fakeMCPManager
	catalog  map[string][]MCPTool
	toolsErr map[string]error
	probes   map[string]MCPProbe
	probeErr map[string]error
}

func (f *fakeMCPInspector) Tools(_ context.Context, name string) ([]MCPTool, error) {
	if err := f.toolsErr[name]; err != nil {
		return nil, err
	}
	return append([]MCPTool(nil), f.catalog[name]...), nil
}

func (f *fakeMCPInspector) Probe(_ context.Context, name string) (MCPProbe, error) {
	if err := f.probeErr[name]; err != nil {
		return MCPProbe{}, err
	}
	return f.probes[name], nil
}

const mcpInspectSecretEnv = "KONSOLAI_MCP_INSPECT_SECRET"

func mcpInspectSecret() string {
	return "sk" + "-" + "5c81f0a2d4b67e93" + "e1f7c3a8"
}

// mcpInspectProfile has one server per classification rule.
func mcpInspectProfile(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(mcpInspectSecretEnv, mcpInspectSecret())
	c := config.Builtin()

	docs := config.DefaultMCPServer()
	docs.Transport = config.MCPTransportHTTP
	docs.URL = "https://example.com/mcp"
	docs.Env = map[string]string{"TOKEN": "${" + mcpInspectSecretEnv + "}"}
	docs.Headers = map[string]string{"Authorization": "Bearer ${" + mcpInspectSecretEnv + "}"}
	docs.Trust = config.MCPTrustAsk
	docs.Tools.ReadOnly = []string{"read_*"}

	locked := config.DefaultMCPServer()
	locked.Transport = config.MCPTransportStdio
	locked.Command = "npx"
	locked.Trust = config.MCPTrustReadOnly

	tight := config.DefaultMCPServer()
	tight.Transport = config.MCPTransportStdio
	tight.Command = "npx"
	tight.Trust = config.MCPTrustAsk
	tight.Tools.Allow = []string{"read_*", "danger"}
	tight.Tools.Deny = []string{"danger"}
	tight.Tools.ReadOnly = []string{"read_*"}

	autotrust := config.DefaultMCPServer()
	autotrust.Transport = config.MCPTransportStdio
	autotrust.Command = "npx"
	autotrust.Trust = config.MCPTrustAuto

	off := config.DefaultMCPServer()
	off.Transport = config.MCPTransportStdio
	off.Command = "npx"
	off.Enabled = false

	c.MCP.Servers = map[string]config.MCPServer{
		"docs":   docs,
		"locked": locked,
		"tight":  tight,
		"auto":   autotrust,
		"off":    off,
	}
	return c
}

func mcpInspectEnv(t *testing.T) (*Env, *strings.Builder) {
	t.Helper()
	env, _, _ := testEnv(t)
	var out strings.Builder
	env.Out = &out
	env.Err = &out
	env.Profile = mcpInspectProfile(t)
	return env, &out
}

func mcpInspectInspector() *fakeMCPInspector {
	return &fakeMCPInspector{
		fakeMCPManager: fakeMCPManager{
			"docs":   {State: "connected", ToolsKnown: true, Tools: 4},
			"locked": {State: "connected", ToolsKnown: true, Tools: 2},
			"tight":  {State: "connected", ToolsKnown: true, Tools: 2},
			"auto":   {State: "connected", ToolsKnown: true, Tools: 2},
			"off":    {State: "disabled"},
		},
		catalog: map[string][]MCPTool{
			"docs": {
				{Name: "read_file", Description: "Read a file from the vault\nSecond line is dropped", ReadOnly: true},
				{Name: "write_file", Description: "Write a file to the vault", ReadOnly: false},
			},
			"locked": {
				{Name: "list_things", Description: "List things", ReadOnly: true},
				{Name: "delete_thing", Description: "Delete a thing", ReadOnly: false},
			},
			"tight": {
				{Name: "read_one", Description: "Read one", ReadOnly: false},
				{Name: "secret_tool", Description: "Not in the allow list", ReadOnly: false},
				{Name: "danger", Description: "Denied outright", ReadOnly: false},
			},
			"auto": {
				{Name: "alpha", Description: "Alpha tool", ReadOnly: true},
				{Name: "beta", Description: "Beta tool", ReadOnly: false},
			},
		},
		probes:   map[string]MCPProbe{"docs": {Latency: 12 * time.Millisecond, Tools: 3}},
		toolsErr: map[string]error{},
		probeErr: map[string]error{},
	}
}

// TestMCPToolsRows covers the classification rules: trust read_only hides
// mutating tools, allow/deny globs hide by name, read_only globs override a
// missing hint, and auto trust trusts the hint.
func TestMCPToolsRows(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	env.MCP = mcpInspectInspector()

	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 0 {
		t.Fatalf("docs tools exit %d", code)
	}
	text := out.String()
	head := lineWith(text, "docs")
	if !strings.Contains(head, "http") || !strings.Contains(head, "trust ask") {
		t.Fatalf("tools header %q", head)
	}
	read := lineWith(text, "read_file")
	if !strings.Contains(read, "read-only") || !strings.Contains(read, "allowed") || !strings.Contains(read, "Read a file from the vault") {
		t.Fatalf("read_file row %q", read)
	}
	if strings.Contains(read, "Second line") {
		t.Fatalf("description kept a second line: %q", read)
	}
	write := lineWith(text, "write_file")
	if !strings.Contains(write, "mutating") || !strings.Contains(write, "allowed") {
		t.Fatalf("write_file row %q", write)
	}

	out.Reset()
	if code := runMCP(context.Background(), env, []string{"tools", "locked"}); code != 0 {
		t.Fatalf("locked tools exit %d", code)
	}
	text = out.String()
	list := lineWith(text, "list_things")
	if !strings.Contains(list, "read-only") || !strings.Contains(list, "allowed") {
		t.Fatalf("locked read-only row %q", list)
	}
	if !strings.Contains(list, "hint") {
		t.Fatalf("locked row does not name the hint: %q", list)
	}
	del := lineWith(text, "delete_thing")
	if !strings.Contains(del, "hidden") || !strings.Contains(del, "trust read_only") {
		t.Fatalf("locked mutating row %q", del)
	}

	out.Reset()
	if code := runMCP(context.Background(), env, []string{"tools", "tight"}); code != 0 {
		t.Fatalf("tight tools exit %d", code)
	}
	text = out.String()
	one := lineWith(text, "read_one")
	if !strings.Contains(one, "read-only") || !strings.Contains(one, "read_only glob") {
		t.Fatalf("read_one row %q", one)
	}
	secret := lineWith(text, "secret_tool")
	if !strings.Contains(secret, "hidden") || !strings.Contains(secret, "allow") {
		t.Fatalf("secret_tool row %q", secret)
	}
	danger := lineWith(text, "danger ")
	if !strings.Contains(danger, "hidden") || !strings.Contains(danger, "deny") {
		t.Fatalf("danger row %q", danger)
	}

	out.Reset()
	if code := runMCP(context.Background(), env, []string{"tools", "auto"}); code != 0 {
		t.Fatalf("auto tools exit %d", code)
	}
	text = out.String()
	alpha := lineWith(text, "alpha")
	if !strings.Contains(alpha, "read-only") || !strings.Contains(alpha, "hint") {
		t.Fatalf("auto hint row %q", alpha)
	}
	beta := lineWith(text, "beta")
	if !strings.Contains(beta, "mutating") || !strings.Contains(beta, "allowed") {
		t.Fatalf("auto mutating row %q", beta)
	}
}

func TestMCPToolsLongDescriptionIsCapped(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	full := strings.Repeat("long description ", 20)
	in := mcpInspectInspector()
	in.catalog["docs"] = []MCPTool{{Name: "long_tool", Description: full, ReadOnly: true}}
	env.MCP = in
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := lineWith(out.String(), "long_tool")
	if strings.Contains(line, full) {
		t.Fatal("the description was not capped")
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("the capped line has no marker: %q", line)
	}
}

func TestMCPToolsErrors(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	inspector := mcpInspectInspector()
	env.MCP = inspector

	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"usage", []string{"tools"}, 2, "usage: /mcp tools <name>"},
		{"unknown", []string{"tools", "nope"}, 2, "no MCP server"},
		{"disabled", []string{"tools", "off"}, 1, "disabled"},
	}
	for _, tc := range cases {
		if code := runMCP(context.Background(), env, tc.args); code != tc.code {
			t.Errorf("%s exit %d", tc.name, code)
		}
	}
	env.MCP = nil
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 1 {
		t.Errorf("no manager exit %d", code)
	}
	if !strings.Contains(out.String(), "no MCP manager") {
		t.Fatalf("no-manager output missing: %q", out.String())
	}

	env.MCP = fakeMCPManager{"docs": {State: "connected"}}
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 1 {
		t.Errorf("plain manager exit %d", code)
	}
	if !strings.Contains(out.String(), "cannot list tools") {
		t.Fatalf("plain-manager output missing: %q", out.String())
	}

	env.MCP = inspector
	inspector.toolsErr["docs"] = errors.New("connect refused")
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 1 {
		t.Errorf("down exit %d", code)
	}
	if !strings.Contains(out.String(), "connect refused") {
		t.Fatalf("down output missing: %q", out.String())
	}
}

func TestMCPToolsUnresolvedEnvNamesTheVariable(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("KONSOLAI_MCP_MISSING", "")
	env, out := mcpInspectEnv(t)
	server, _ := env.Profile.MCPServer("docs")
	server.Env = map[string]string{"TOKEN": "${KONSOLAI_MCP_MISSING}"}
	env.Profile.MCP.Servers["docs"] = server
	inspector := mcpInspectInspector()
	inspector.toolsErr["docs"] = errors.New(`environment variable "KONSOLAI_MCP_MISSING" is not set`)
	env.MCP = inspector
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "KONSOLAI_MCP_MISSING") {
		t.Fatalf("the variable name is missing: %q", text)
	}
	if strings.Contains(text, mcpInspectSecret()) {
		t.Fatal("the output printed a secret value")
	}
}

func TestMCPToolsDoesNotLeakValues(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	inspector := mcpInspectInspector()
	inspector.catalog["docs"] = []MCPTool{{
		Name:        "leaky",
		Description: "server echoed " + mcpInspectSecret(),
		ReadOnly:    true,
	}}
	env.MCP = inspector
	if code := runMCP(context.Background(), env, []string{"tools", "docs"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	if strings.Contains(text, mcpInspectSecret()) {
		t.Fatal("/mcp tools printed a secret value")
	}
	if strings.Contains(text, os.Getenv(mcpInspectSecretEnv)) {
		t.Fatal("/mcp tools resolved an env reference")
	}
}

func TestMCPTestReportsLatencyAndTools(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	env.MCP = mcpInspectInspector()
	if code := runMCP(context.Background(), env, []string{"test", "docs"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "connected") || !strings.Contains(text, "12ms") || !strings.Contains(text, "3 tools") {
		t.Fatalf("test output %q", text)
	}
	if strings.Contains(text, "${") || strings.Contains(text, mcpInspectSecret()) {
		t.Fatal("/mcp test printed a reference or a value")
	}
}

func TestMCPTestErrors(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	inspector := mcpInspectInspector()
	inspector.probeErr["docs"] = errors.New("connect refused")
	env.MCP = inspector

	if code := runMCP(context.Background(), env, []string{"test"}); code != 2 {
		t.Errorf("usage exit %d", code)
	}
	if code := runMCP(context.Background(), env, []string{"test", "nope"}); code != 2 {
		t.Errorf("unknown exit %d", code)
	}
	if code := runMCP(context.Background(), env, []string{"test", "off"}); code != 1 {
		t.Errorf("disabled exit %d", code)
	}
	if code := runMCP(context.Background(), env, []string{"test", "docs"}); code != 1 {
		t.Errorf("down exit %d", code)
	}
	if !strings.Contains(out.String(), "connect refused") {
		t.Fatalf("down output %q", out.String())
	}
}

func TestMCPTestUnresolvedEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("KONSOLAI_MCP_MISSING", "")
	env, out := mcpInspectEnv(t)
	server, _ := env.Profile.MCPServer("docs")
	server.Headers = map[string]string{"Authorization": "Bearer ${KONSOLAI_MCP_MISSING}"}
	env.Profile.MCP.Servers["docs"] = server
	inspector := mcpInspectInspector()
	inspector.probeErr["docs"] = errors.New(`environment variable "KONSOLAI_MCP_MISSING" is not set`)
	env.MCP = inspector
	if code := runMCP(context.Background(), env, []string{"test", "docs"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "KONSOLAI_MCP_MISSING") {
		t.Fatalf("the variable name is missing: %q", text)
	}
}

func TestMCPInspectUsageAndTree(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out := mcpInspectEnv(t)
	env.MCP = mcpInspectInspector()
	if code := runMCP(context.Background(), env, []string{"bogus"}); code != 2 {
		t.Fatalf("bogus exit %d", code)
	}
	usage := out.String()
	for _, want := range []string{"tools", "test"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("usage %q does not mention %s", usage, want)
		}
	}
	tree := Default().Lookup("mcp").Tree(env)
	for _, want := range []string{"list", "tools", "test"} {
		if _, ok := findChoice(tree, want); !ok {
			t.Fatalf("/mcp tree is missing %s", want)
		}
	}
}
