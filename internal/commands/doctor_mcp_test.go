package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestDoctorMCPRows(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	env, out, home := doctorEnv(t, srv.URL+"/v1")
	doctorHome = func() (string, error) { return home, nil }

	files := config.DefaultMCPServer()
	files.Transport = config.MCPTransportStdio
	files.Command = "npx"
	web := config.DefaultMCPServer()
	web.Transport = config.MCPTransportHTTP
	web.URL = "https://example.com/mcp"
	web.Trust = config.MCPTrustAuto
	web.Env = map[string]string{"DOCS_KEY": "${MCP_DOCTOR_TOKEN}"}
	c := env.Profile
	c.MCP.Servers = map[string]config.MCPServer{"files": files, "web": web}
	env.Profile = c
	env.MCP = fakeMCPManager{
		"files": {State: "connected", Tools: 2, ToolsKnown: true, ToolNames: []string{"read_file", "write_file"}},
		"web":   {State: "down", Err: "connect failed", MissingEnv: []string{"MCP_DOCTOR_TOKEN"}},
	}

	_ = runDoctor(context.Background(), env, nil)
	text := out.String()
	filesLine := doctorLine(text, "mcp files")
	if !strings.Contains(filesLine, "connected") || !strings.Contains(filesLine, "stdio") ||
		!strings.Contains(filesLine, "trust ask") || !strings.Contains(filesLine, "2 tools") {
		t.Fatalf("files row %q", filesLine)
	}
	if !strings.Contains(text, "mcp tool") || !strings.Contains(text, "files: read_file") {
		t.Fatalf("doctor misses the server: tool line:\n%s", text)
	}
	webLine := doctorLine(text, "mcp web")
	if !strings.HasPrefix(webLine, "!") || !strings.Contains(webLine, "down") ||
		!strings.Contains(webLine, "missing MCP_DOCTOR_TOKEN") {
		t.Fatalf("web row %q", webLine)
	}
	if !strings.Contains(text, "export MCP_DOCTOR_TOKEN") {
		t.Fatalf("doctor misses the env fix:\n%s", text)
	}
	if strings.Contains(text, "${") || strings.Contains(text, "DOCS_KEY=") {
		t.Fatalf("doctor printed an env value or reference:\n%s", text)
	}
}

func TestDoctorHidesMCPWhenNoneConfigured(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	env, out, home := doctorEnv(t, srv.URL+"/v1")
	doctorHome = func() (string, error) { return home, nil }
	_ = runDoctor(context.Background(), env, nil)
	if strings.Contains(out.String(), "mcp ") {
		t.Fatalf("doctor printed an MCP row with no server configured:\n%s", out.String())
	}
}
