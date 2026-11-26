package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures are assembled at runtime so secret scanners never flag a literal.
const mcpConfigLeakEnv = "KONSOLAI_MCP_CONFIG_LEAK_SECRET"

func mcpConfigLeakSecret() string {
	return "sk" + "-" + "5d7b2a9e4c1f8063" + "b8e3a6d2"
}

// TestShowAndDiffNeverPrintTheResolvedMCPSecret covers the config render side of
// /config show and /config diff: a ${ENV} reference stays a reference and any
// literal or mixed value is masked, so the resolved secret never prints.
func TestShowAndDiffNeverPrintTheResolvedMCPSecret(t *testing.T) {
	secret := mcpConfigLeakSecret()
	t.Setenv(mcpConfigLeakEnv, secret)
	c := Builtin()
	envsrv := DefaultMCPServer()
	envsrv.Transport = MCPTransportStdio
	envsrv.Command = "npx"
	envsrv.Env = map[string]string{
		"LEAK_TOKEN": "${" + mcpConfigLeakEnv + "}",
		"PLAIN":      "not-a-secret",
	}
	httpsrv := DefaultMCPServer()
	httpsrv.Transport = MCPTransportHTTP
	httpsrv.URL = "https://example.com/mcp"
	httpsrv.Headers = map[string]string{"Authorization": "Bearer ${" + mcpConfigLeakEnv + "}"}
	c.MCP.Servers = map[string]MCPServer{"envsrv": envsrv, "httpsrv": httpsrv}

	shown, err := c.ShowYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shown, secret) {
		t.Fatalf("ShowYAML printed the resolved secret:\n%s", shown)
	}
	if !strings.Contains(shown, "LEAK_TOKEN: ${"+mcpConfigLeakEnv+"}") {
		t.Fatalf("ShowYAML hid the reference instead of keeping it:\n%s", shown)
	}
	if !strings.Contains(shown, "Authorization: '***'") || strings.Contains(shown, "Bearer") {
		t.Fatalf("ShowYAML did not mask the mixed header:\n%s", shown)
	}
	if strings.Contains(shown, "not-a-secret") {
		t.Fatalf("ShowYAML printed a literal value:\n%s", shown)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "profiles:\n  default:\n    mcp:\n      servers:\n" +
		"        envsrv:\n          transport: stdio\n          command: npx\n          env:\n            LEAK_TOKEN: \"${" + mcpConfigLeakEnv + "}\"\n" +
		"        httpsrv:\n          transport: http\n          url: https://example.com/mcp\n          headers:\n            Authorization: \"Bearer ${" + mcpConfigLeakEnv + "}\"\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := DiffText(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, secret) {
		t.Fatalf("DiffText printed the resolved secret:\n%s", diff)
	}
	if !strings.Contains(diff, "mcp.servers.envsrv.env.LEAK_TOKEN") || !strings.Contains(diff, "mcp.servers.httpsrv.headers.Authorization") {
		t.Fatalf("DiffText misses the masked env and header rows:\n%s", diff)
	}
	if !strings.Contains(diff, "***") || strings.Contains(diff, "Bearer") {
		t.Fatalf("DiffText did not mask a value:\n%s", diff)
	}
}
