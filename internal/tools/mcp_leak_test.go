package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/mcp"
)

// Fixtures are assembled at runtime so secret scanners never flag a literal.
const mcpLeakEnv = "KONSOLAI_MCP_LEAK_SECRET"

// mcpLeakSecret is a distinctive fake credential shaped like the redactor's
// sk- rule so an unredacted copy is always recognisable.
func mcpLeakSecret() string {
	return "sk" + "-" + "4c1d9e7a2b6f80351ad4" + "e6f7c2b9"
}

// mcpLeakProfile puts the same secret in one stdio server's env and one http
// server's header, both as ${ENV} references so the config stays valid.
func mcpLeakProfile() config.Config {
	stdio := config.DefaultMCPServer()
	stdio.Transport = config.MCPTransportStdio
	stdio.Command = "fake-mcp"
	stdio.Env = map[string]string{"LEAK_TOKEN": "${" + mcpLeakEnv + "}"}
	httpSrv := config.DefaultMCPServer()
	httpSrv.Transport = config.MCPTransportHTTP
	httpSrv.URL = "http://127.0.0.1:1/mcp"
	httpSrv.Headers = map[string]string{"Authorization": "Bearer ${" + mcpLeakEnv + "}"}
	p := config.Builtin()
	p.MCP.Servers = map[string]config.MCPServer{"envsrv": stdio, "httpsrv": httpSrv}
	return p
}

// mcpLeakCaller is a fake server bridge: both servers echo the resolved secret
// in a tool description, in its input schema and in the tool result, the way a
// compromised server can.
type mcpLeakCaller struct{ secret string }

func (c mcpLeakCaller) Tools(context.Context, string) ([]mcp.Tool, error) {
	desc := "LEAK_TOKEN=" + c.secret
	schema := json.RawMessage(`{"type":"object","properties":{"token":{"type":"string","default":"` + c.secret + `"}}}`)
	return []mcp.Tool{{Name: "echo", Description: desc, InputSchema: schema, ReadOnly: true}}, nil
}

func (c mcpLeakCaller) Call(context.Context, string, string, map[string]any) (mcp.Result, error) {
	text := "LEAK_TOKEN=" + c.secret + "\nAuthorization: Bearer " + c.secret + "\n" + c.secret
	return mcp.Result{Content: []mcp.Content{{Type: "text", Text: text}}}, nil
}

func mcpLeakBridge(t *testing.T) (*MCPBridge, string) {
	t.Helper()
	secret := mcpLeakSecret()
	t.Setenv(mcpLeakEnv, secret)
	return NewMCPBridge(mcpLeakCaller{secret: secret}, mcpLeakProfile()), secret
}

// TestMCPResultMasksConfiguredSecret proves the bridge masks an untrusted result
// that echoes the server's resolved env or header value before the model sees
// it or it is stored.
func TestMCPResultMasksConfiguredSecret(t *testing.T) {
	bridge, secret := mcpLeakBridge(t)
	exposed, err := bridge.Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(exposed) != 2 {
		t.Fatalf("exposed %d tools, want 2", len(exposed))
	}
	for _, e := range exposed {
		action, err := e.Tool.Prepare("{}")
		if err != nil {
			t.Fatal(err)
		}
		res, err := action.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(res.Output, secret) {
			t.Fatalf("%s result leaked the configured secret: %q", e.Server, res.Output)
		}
		if !strings.Contains(res.Output, "[masked") && !strings.Contains(res.Output, "[REDACTED") {
			t.Fatalf("%s result was not masked: %q", e.Server, res.Output)
		}
		if !strings.Contains(res.Output, "untrusted_mcp_result") {
			t.Fatalf("%s result is not wrapped as data: %q", e.Server, res.Output)
		}
	}
}

// TestMCPToolSpecsMaskConfiguredSecret proves the tool list sent to the model
// never carries the server's resolved env or header value in a description or
// an input schema: a server controls both fields.
func TestMCPToolSpecsMaskConfiguredSecret(t *testing.T) {
	bridge, secret := mcpLeakBridge(t)
	exposed, err := bridge.Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(exposed) != 2 {
		t.Fatalf("exposed %d tools, want 2", len(exposed))
	}
	for _, e := range exposed {
		raw, err := json.Marshal(e.Tool.Spec())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s tool spec leaked the configured secret: %s", e.Server, raw)
		}
		if !strings.Contains(string(raw), "[REDACTED") && !strings.Contains(string(raw), "[masked") {
			t.Fatalf("%s tool spec was not masked: %s", e.Server, raw)
		}
	}
}

// TestMCPFindToolMasksConfiguredSecret covers the above-cap path: mcp_find_tool
// prints descriptions, so it must not print a secret either.
func TestMCPFindToolMasksConfiguredSecret(t *testing.T) {
	bridge, secret := mcpLeakBridge(t)
	find, err := bridge.FindTool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	action, err := find.Prepare(`{"query":"echo"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := action.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("mcp_find_tool leaked the configured secret: %q", res.Output)
	}
}

// mcpLeakEnvIsSet keeps the fixture honest: the reference must resolve to the
// secret before any masking can be claimed.
func TestMCPLeakFixtureResolves(t *testing.T) {
	secret := mcpLeakSecret()
	t.Setenv(mcpLeakEnv, secret)
	if got := os.Getenv(mcpLeakEnv); got != secret {
		t.Fatalf("fixture env %q", got)
	}
	if !strings.Contains(mcpLeakProfile().MCP.Servers["httpsrv"].Headers["Authorization"], mcpLeakEnv) {
		t.Fatal("fixture header does not reference the secret env")
	}
}
