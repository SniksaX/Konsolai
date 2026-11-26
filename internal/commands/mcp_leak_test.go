package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// Fixtures are assembled at runtime so secret scanners never flag a literal.
const mcpCmdLeakEnv = "KONSOLAI_MCP_CMD_LEAK_SECRET"

func mcpCmdLeakSecret() string {
	return "sk" + "-" + "a91c4f2e7b3d8065" + "d2e6f1a9"
}

// mcpCmdLeakProfile carries the secret in one stdio server's env and one http
// server's header, both as ${ENV} references.
func mcpCmdLeakProfile(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(mcpCmdLeakEnv, mcpCmdLeakSecret())
	c := config.Builtin()
	envsrv := config.DefaultMCPServer()
	envsrv.Transport = config.MCPTransportStdio
	envsrv.Command = "npx"
	envsrv.Env = map[string]string{"LEAK_TOKEN": "${" + mcpCmdLeakEnv + "}"}
	httpsrv := config.DefaultMCPServer()
	httpsrv.Transport = config.MCPTransportHTTP
	httpsrv.URL = "https://example.com/mcp"
	httpsrv.Headers = map[string]string{"Authorization": "Bearer ${" + mcpCmdLeakEnv + "}"}
	c.MCP.Servers = map[string]config.MCPServer{"envsrv": envsrv, "httpsrv": httpsrv}
	return c
}

func assertNoMCPCmdSecret(t *testing.T, where, text string) {
	t.Helper()
	if strings.Contains(text, mcpCmdLeakSecret()) {
		t.Fatalf("%s printed the configured MCP secret", where)
	}
}

// TestStatusMCPRowsDoNotLeakTheSecret is leak surface 2 for /status.
func TestStatusMCPRowsDoNotLeakTheSecret(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	env, out, _ := testEnv(t)
	env.Profile = mcpCmdLeakProfile(t)
	env.MCP = fakeMCPManager{
		"envsrv":  {State: "connected", Tools: 2, ToolsKnown: true, ToolNames: []string{"read", "write"}},
		"httpsrv": {State: "down", Err: "connect failed", MissingEnv: []string{"MCP_OTHER_TOKEN"}},
	}
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	assertNoMCPCmdSecret(t, "/status", text)
	// The rows are real evidence the surface ran: name, state and the missing
	// env *name* appear, but no value from the profile does.
	for _, want := range []string{"envsrv", "httpsrv", "connected", "down", "missing MCP_OTHER_TOKEN"} {
		if !strings.Contains(text, want) {
			t.Fatalf("/status misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "${") || strings.Contains(text, "Bearer") || strings.Contains(text, "npx") {
		t.Fatalf("/status printed a configured value or reference:\n%s", text)
	}
}

// TestDoctorMCPRowsDoNotLeakTheSecret is leak surface 2 for /doctor.
func TestDoctorMCPRowsDoNotLeakTheSecret(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	env, out, home := doctorEnv(t, "http://127.0.0.1:9/v1")
	doctorHome = func() (string, error) { return home, nil }
	env.Profile = mcpCmdLeakProfile(t)
	env.MCP = fakeMCPManager{
		"envsrv":  {State: "connected", Tools: 2, ToolsKnown: true, ToolNames: []string{"read", "write"}},
		"httpsrv": {State: "down", Err: "connect failed", MissingEnv: []string{"MCP_OTHER_TOKEN"}},
	}
	_ = runDoctor(context.Background(), env, nil)
	text := out.String()
	assertNoMCPCmdSecret(t, "/doctor", text)
	for _, want := range []string{"mcp envsrv", "mcp httpsrv", "connected", "down", "missing MCP_OTHER_TOKEN", "export MCP_OTHER_TOKEN"} {
		if !strings.Contains(text, want) {
			t.Fatalf("/doctor misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "${") || strings.Contains(text, "Bearer") || strings.Contains(text, "npx") {
		t.Fatalf("/doctor printed a configured value or reference:\n%s", text)
	}
}

// TestConfigShowAndDiffMaskTheMCPSecret is leak surface 3: /config show and
// /config diff render env and header values masked.
func TestConfigShowAndDiffMaskTheMCPSecret(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	profile := mcpCmdLeakProfile(t)

	env, out, _ := testEnv(t)
	env.Profile = profile
	if code := runConfig(context.Background(), env, []string{"show"}); code != 0 {
		t.Fatalf("/config show exit %d", code)
	}
	shown := out.String()
	assertNoMCPCmdSecret(t, "/config show", shown)
	if !strings.Contains(shown, "mcp:") || !strings.Contains(shown, "envsrv") {
		t.Fatalf("/config show misses the mcp section:\n%s", shown)
	}
	if !strings.Contains(shown, "***") {
		t.Fatalf("/config show did not mask a mixed header:\n%s", shown)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	file := "profiles:\n  default:\n    mcp:\n      servers:\n" +
		"        envsrv:\n          transport: stdio\n          command: npx\n          env:\n            LEAK_TOKEN: \"${" + mcpCmdLeakEnv + "}\"\n" +
		"        httpsrv:\n          transport: http\n          url: https://example.com/mcp\n          headers:\n            Authorization: \"Bearer ${" + mcpCmdLeakEnv + "}\"\n"
	if err := os.WriteFile(path, []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	env2, out2, _ := testEnv(t)
	env2.Profile = profile
	env2.ConfigPath = path
	if code := runConfig(context.Background(), env2, []string{"diff"}); code != 0 {
		t.Fatalf("/config diff exit %d", code)
	}
	diff := out2.String()
	assertNoMCPCmdSecret(t, "/config diff", diff)
	if !strings.Contains(diff, "mcp.servers.envsrv") || !strings.Contains(diff, "mcp.servers.httpsrv") {
		t.Fatalf("/config diff misses the server changes:\n%s", diff)
	}
	if !strings.Contains(diff, "***") {
		t.Fatalf("/config diff did not mask a value:\n%s", diff)
	}
	if strings.Contains(diff, "Bearer") {
		t.Fatalf("/config diff printed a mixed header:\n%s", diff)
	}
}
