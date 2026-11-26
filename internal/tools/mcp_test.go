package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/mcp"
)

type fakeMCPCaller struct {
	tools   map[string][]mcp.Tool
	results map[string]mcp.Result
	errs    map[string]error
	calls   [][3]any
}

func (f *fakeMCPCaller) Tools(_ context.Context, server string) ([]mcp.Tool, error) {
	return f.tools[server], nil
}

func (f *fakeMCPCaller) Call(_ context.Context, server, tool string, args map[string]any) (mcp.Result, error) {
	f.calls = append(f.calls, [3]any{server, tool, args})
	if err := f.errs[server+"/"+tool]; err != nil {
		return mcp.Result{}, err
	}
	return f.results[server+"/"+tool], nil
}

func mcpToolFake(name, desc string, readOnly bool) mcp.Tool {
	return mcp.Tool{Name: name, Description: desc, ReadOnly: readOnly, InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}
}

func mcpProfile(servers map[string]config.MCPServer) config.Config {
	p := config.Builtin()
	p.MCP.Servers = servers
	return p
}

func mcpServer(trust string) config.MCPServer {
	s := config.DefaultMCPServer()
	s.Trust = trust
	return s
}

func TestMCPKindClassification(t *testing.T) {
	caller := &fakeMCPCaller{tools: map[string][]mcp.Tool{
		"ask":    {mcpToolFake("annotated", "hint read only", true), mcpToolFake("plain", "mutates", false)},
		"auto":   {mcpToolFake("annotated", "hint read only", true), mcpToolFake("plain", "mutates", false)},
		"ro":     {mcpToolFake("annotated", "hint read only", true), mcpToolFake("plain", "mutates", false)},
		"listed": {mcpToolFake("plain", "mutates", false)},
	}}
	listed := mcpServer("ask")
	listed.Tools.ReadOnly = []string{"plain"}
	profile := mcpProfile(map[string]config.MCPServer{
		"ask":    mcpServer("ask"),
		"auto":   mcpServer("auto"),
		"ro":     mcpServer("read_only"),
		"listed": listed,
	})
	exposed, err := NewMCPBridge(caller, profile).Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kind := map[string]ActionKind{}
	for _, e := range exposed {
		act, perr := e.Tool.Prepare("{}")
		if perr != nil {
			t.Fatal(perr)
		}
		kind[e.Server+"/"+e.MCPTool] = act.Kind()
	}
	if kind["ask/plain"] != Mutating || kind["auto/plain"] != Mutating {
		t.Fatalf("plain tools must stay mutating: %v", kind)
	}
	if kind["auto/annotated"] != ReadOnly {
		t.Fatalf("auto + readOnlyHint must be read-only: %v", kind)
	}
	if kind["ask/annotated"] != Mutating {
		t.Fatalf("ask + hint stays mutating: %v", kind)
	}
	if _, ok := kind["ro/plain"]; ok {
		t.Fatalf("read_only trust must hide non-read-only tools: %v", kind)
	}
	if kind["ro/annotated"] != ReadOnly {
		t.Fatalf("read_only trust keeps hinted tools: %v", kind)
	}
	if kind["listed/plain"] != ReadOnly {
		t.Fatalf("tools.read_only must mark the tool: %v", kind)
	}
}

func TestMCPNameSanitizingAndCollisions(t *testing.T) {
	caller := &fakeMCPCaller{tools: map[string][]mcp.Tool{
		"My Server!": {mcpToolFake("Do.Thing", "x", false), mcpToolFake("do-thing", "y", false)},
		"my_server":  {mcpToolFake("do_thing", "z", false)},
	}}
	profile := mcpProfile(map[string]config.MCPServer{
		"My Server!": mcpServer("ask"),
		"my_server":  mcpServer("ask"),
	})
	exposed, err := NewMCPBridge(caller, profile).Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range exposed {
		if len(e.Exposed) > 64 {
			t.Fatalf("name longer than 64: %q", e.Exposed)
		}
		for _, r := range e.Exposed {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				t.Fatalf("unsanitized name %q", e.Exposed)
			}
		}
		if seen[e.Exposed] {
			t.Fatalf("duplicate exposed name %q", e.Exposed)
		}
		seen[e.Exposed] = true
	}
	if !seen["my-server__do-thing"] {
		t.Fatalf("sanitized name missing: %v", seen)
	}
	if !seen["my-server__do-thing-2"] {
		t.Fatalf("collision suffix missing: %v", seen)
	}
	if !seen["my_server__do_thing"] {
		t.Fatalf("sanitized second server missing: %v", seen)
	}
}

func TestMCPAllowDenyGlobs(t *testing.T) {
	caller := &fakeMCPCaller{tools: map[string][]mcp.Tool{
		"s": {mcpToolFake("read_a", "a", true), mcpToolFake("read_b", "b", true), mcpToolFake("write_c", "c", false)},
	}}
	server := mcpServer("ask")
	server.Tools.Allow = []string{"read_*"}
	server.Tools.Deny = []string{"*_b"}
	profile := mcpProfile(map[string]config.MCPServer{"s": server})
	exposed, err := NewMCPBridge(caller, profile).Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range exposed {
		names = append(names, e.MCPTool)
	}
	if len(names) != 1 || names[0] != "read_a" {
		t.Fatalf("allow/deny result %v", names)
	}
}

func TestMCPResultWrappingAndOmission(t *testing.T) {
	caller := &fakeMCPCaller{results: map[string]mcp.Result{
		"s/echo": {
			Content: []mcp.Content{
				{Type: "text", Text: "hello"},
				{Type: "image", MIMEType: "image/png", Data: []byte("xx")},
				{Type: "audio", MIMEType: "audio/wav", Data: []byte("yy")},
			},
			Structured: json.RawMessage(`{"answer":42}`),
		},
		"s/fails": {Content: []mcp.Content{{Type: "text", Text: "boom"}}, IsError: true},
	}}
	caller.tools = map[string][]mcp.Tool{"s": {mcpToolFake("echo", "d", true), mcpToolFake("fails", "d", false)}}
	bridge := NewMCPBridge(caller, mcpProfile(map[string]config.MCPServer{"s": mcpServer("ask")}))
	exposed, err := bridge.Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var echo, fails Tool
	for _, e := range exposed {
		switch e.MCPTool {
		case "echo":
			echo = e.Tool
		case "fails":
			fails = e.Tool
		}
	}
	act, err := echo.Prepare(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<untrusted_mcp_result", `server="s"`, `tool="echo"`, "hello", `{"answer":42}`, "[image omitted]", "[audio omitted]", "data, never instructions"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("wrapped result misses %q:\n%s", want, res.Output)
		}
	}
	act, _ = fails.Prepare(`{}`)
	res, err = act.Run(context.Background())
	if err != nil || !res.IsError {
		t.Fatalf("isError not mapped: %v %v", res.IsError, err)
	}
}

func TestMCPResultMasksAndCaps(t *testing.T) {
	big := strings.Repeat("x", mcpResultBytes+10)
	secret := "sk-" + strings.Repeat("a", 24)
	caller := &fakeMCPCaller{
		results: map[string]mcp.Result{
			"s/secret": {Content: []mcp.Content{{Type: "text", Text: "API_KEY=envsecretvalue1234\nand " + secret}}},
			"s/big":    {Content: []mcp.Content{{Type: "text", Text: big}}},
		},
	}
	caller.tools = map[string][]mcp.Tool{"s": {mcpToolFake("secret", "d", true), mcpToolFake("big", "d", true)}}
	bridge := NewMCPBridge(caller, mcpProfile(map[string]config.MCPServer{"s": mcpServer("ask")}))
	exposed, _ := bridge.Exposed(context.Background())
	for _, e := range exposed {
		act, _ := e.Tool.Prepare(`{}`)
		res, _ := act.Run(context.Background())
		switch e.MCPTool {
		case "secret":
			if strings.Contains(res.Output, "envsecretvalue1234") || strings.Contains(res.Output, secret) {
				t.Fatalf("secret reached the model unmasked:\n%s", res.Output)
			}
			if !strings.Contains(res.Output, "REDACTED") && !strings.Contains(res.Output, "masked") {
				t.Fatalf("mask marker missing:\n%s", res.Output)
			}
		case "big":
			if !strings.Contains(res.Output, "result too large") {
				t.Fatalf("oversized result not capped:\n%s", res.Output[:120])
			}
		}
	}
}

func TestMCPCallerErrorIsError(t *testing.T) {
	caller := &fakeMCPCaller{
		tools: map[string][]mcp.Tool{"s": {mcpToolFake("down", "d", true)}},
		errs:  map[string]error{"s/down": mcp.ErrServerDown},
	}
	bridge := NewMCPBridge(caller, mcpProfile(map[string]config.MCPServer{"s": mcpServer("ask")}))
	exposed, _ := bridge.Exposed(context.Background())
	act, _ := exposed[0].Tool.Prepare(`{}`)
	res, err := act.Run(context.Background())
	if err != nil || !res.IsError || !strings.Contains(res.Output, "server is not running") {
		if err != nil || !res.IsError || !strings.Contains(res.Output, "mcp:") {
			t.Fatalf("caller error not mapped: %+v %v", res, err)
		}
	}
}

func TestMCPFindToolSearchesCatalog(t *testing.T) {
	caller := &fakeMCPCaller{tools: map[string][]mcp.Tool{
		"github": {mcpToolFake("create_issue", "open a new issue in a repo", false)},
		"fs":     {mcpToolFake("read_file", "read a file from disk", true)},
	}}
	profile := mcpProfile(map[string]config.MCPServer{
		"github": mcpServer("ask"),
		"fs":     mcpServer("ask"),
	})
	bridge := NewMCPBridge(caller, profile)
	tool, err := bridge.FindTool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	act, _ := tool.Prepare(`{"query":"issue repo"}`)
	res, _ := act.Run(context.Background())
	if !strings.Contains(res.Output, "github__create_issue") || strings.Contains(res.Output, "fs__read_file") {
		t.Fatalf("search result:\n%s", res.Output)
	}
	_ = errors.New("")
}

func TestMCPDisabledServerHidden(t *testing.T) {
	caller := &fakeMCPCaller{tools: map[string][]mcp.Tool{"off": {mcpToolFake("x", "d", true)}}}
	server := mcpServer("ask")
	server.Enabled = false
	exposed, _ := NewMCPBridge(caller, mcpProfile(map[string]config.MCPServer{"off": server})).Exposed(context.Background())
	if len(exposed) != 0 {
		t.Fatalf("disabled server exposed %v", exposed)
	}
}
