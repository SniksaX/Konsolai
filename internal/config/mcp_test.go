package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadMCP(t *testing.T, body string) (Config, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f, err := Parse([]byte(body), "mcp.yaml")
	if err != nil {
		return Config{}, err
	}
	return f.Resolve("")
}

func loadMCPProfile(t *testing.T, body, profile string) (Config, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f, err := Parse([]byte(body), "mcp.yaml")
	if err != nil {
		return Config{}, err
	}
	return f.Resolve(profile)
}

func TestMCPDefaultsAndLoad(t *testing.T) {
	if got := Builtin().MCP.MaxInlineTools; got != 15 {
		t.Fatalf("max_inline_tools %d", got)
	}
	if len(Builtin().MCP.Servers) != 0 {
		t.Fatal("built-in mcp servers are not empty")
	}

	body := `defaults:
  mcp:
    max_inline_tools: 4
    servers:
      files:
        transport: stdio
        command: npx
        args: ["-y", "@example/files"]
        env:
          ROOT: /tmp
          API_KEY: "${FILES_KEY}"
        enabled: true
        trust: read_only
        timeout_ms: 5000
        private: true
        tools:
          allow: ["read_*", "list_*"]
          deny: ["write_*"]
          read_only: ["read_*"]
      web:
        transport: http
        url: https://example.com/mcp
        headers:
          Authorization: "Bearer ${WEB_TOKEN}"
        trust: auto
`
	c, err := loadMCP(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if c.MCP.MaxInlineTools != 4 {
		t.Fatalf("max_inline_tools %d", c.MCP.MaxInlineTools)
	}
	files, ok := c.MCPServer("files")
	if !ok {
		t.Fatal("files server missing")
	}
	if files.Transport != MCPTransportStdio || files.Command != "npx" || len(files.Args) != 2 {
		t.Fatalf("files %+v", files)
	}
	if files.Env["ROOT"] != "/tmp" || files.Env["API_KEY"] != "${FILES_KEY}" {
		t.Fatalf("files env %v", files.Env)
	}
	if !files.Enabled || files.TrustMode() != MCPTrustReadOnly || files.TimeoutMS != 5000 || !files.Private {
		t.Fatalf("files flags %+v", files)
	}
	if len(files.Tools.Allow) != 2 || files.Tools.Deny[0] != "write_*" || files.Tools.ReadOnly[0] != "read_*" {
		t.Fatalf("files tools %+v", files.Tools)
	}
	web, ok := c.MCPServer("web")
	if !ok {
		t.Fatal("web server missing")
	}
	if web.Transport != MCPTransportHTTP || web.URL != "https://example.com/mcp" {
		t.Fatalf("web %+v", web)
	}
	if web.Headers["Authorization"] != "Bearer ${WEB_TOKEN}" {
		t.Fatalf("web headers %v", web.Headers)
	}
	if web.TrustMode() != MCPTrustAuto {
		t.Fatalf("web trust %q", web.Trust)
	}
}

func TestMCPDefaultsWhenFieldsAreOmitted(t *testing.T) {
	c, err := loadMCP(t, `defaults:
  mcp:
    servers:
      local:
        transport: http
        url: http://127.0.0.1:8080/mcp
`)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.MCPServer("local")
	if !s.Enabled {
		t.Fatal("enabled should default to true")
	}
	if s.TrustMode() != MCPTrustAsk {
		t.Fatalf("trust %q", s.Trust)
	}
	if s.TimeoutMS != MCPDefaultTimeoutMS {
		t.Fatalf("timeout %d", s.TimeoutMS)
	}
	if s.Private {
		t.Fatal("private should default to false")
	}
	if c.MCP.MaxInlineTools != 15 {
		t.Fatalf("max_inline_tools %d", c.MCP.MaxInlineTools)
	}
}

func TestMCPProfileOverlayMergesServer(t *testing.T) {
	c, err := loadMCPProfile(t, `defaults:
  mcp:
    servers:
      files:
        transport: stdio
        command: npx
        enabled: false
profiles:
  quiet:
    mcp:
      servers:
        files:
          trust: auto
`, "quiet")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := c.MCPServer("files")
	if s.Enabled {
		t.Fatal("the profile overlay re-enabled the server")
	}
	if s.TrustMode() != MCPTrustAuto || s.Command != "npx" {
		t.Fatalf("merged %+v", s)
	}
}

func TestMCPRejectsUnknownKeys(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"server field", `defaults:
  mcp:
    servers:
      bad:
        transport: stdio
        command: npx
        shell: true
`, "unknown key"},
		{"tools field", `defaults:
  mcp:
    servers:
      bad:
        transport: stdio
        command: npx
        tools:
          include: ["x"]
`, "unknown key"},
		{"mcp field", `defaults:
  mcp:
    max_inline_tool: 3
`, "unknown key"},
		{"bad server name", `defaults:
  mcp:
    servers:
      "has space":
        transport: stdio
        command: npx
`, "not a valid server name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadMCP(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMCPTransportAndTrustValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"stdio no command", `defaults:
  mcp:
    servers:
      s: {transport: stdio}
`, "command is required"},
		{"stdio with url", `defaults:
  mcp:
    servers:
      s: {transport: stdio, command: npx, url: https://example.com/mcp}
`, "url must be empty"},
		{"http no url", `defaults:
  mcp:
    servers:
      s: {transport: http}
`, "not an HTTP URL"},
		{"http with command", `defaults:
  mcp:
    servers:
      s: {transport: http, command: npx, url: https://example.com/mcp}
`, "command must be empty"},
		{"bad transport", `defaults:
  mcp:
    servers:
      s: {transport: carrier-pigeon, command: npx}
`, "transport must be stdio or http"},
		{"http remote plain", `defaults:
  mcp:
    servers:
      s: {transport: http, url: http://203.0.113.9/mcp}
`, "must use https"},
		{"bad trust", `defaults:
  mcp:
    servers:
      s: {transport: stdio, command: npx, trust: yolo}
`, "trust must be ask"},
		{"zero timeout", `defaults:
  mcp:
    servers:
      s: {transport: stdio, command: npx, timeout_ms: 0}
`, "timeout_ms must be a positive integer"},
		{"bad glob", `defaults:
  mcp:
    servers:
      s:
        transport: stdio
        command: npx
        tools: {allow: ["read_["]}
`, "invalid tool glob"},
		{"empty glob", `defaults:
  mcp:
    servers:
      s:
        transport: stdio
        command: npx
        tools: {deny: [""]}
`, "must not be empty"},
		{"bad env name", `defaults:
  mcp:
    servers:
      s:
        transport: stdio
        command: npx
        env: {"BAD-NAME": x}
`, "invalid environment variable name"},
		{"bad header name", `defaults:
  mcp:
    servers:
      s:
        transport: http
        url: https://example.com/mcp
        headers: {"Bad Header": x}
`, "invalid header name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadMCP(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
	// A loopback or private plain-http endpoint is allowed.
	for _, url := range []string{"http://127.0.0.1:8080/mcp", "http://10.1.2.3:8080/mcp", "https://example.com/mcp"} {
		body := "defaults:\n  mcp:\n    servers:\n      s: {transport: http, url: " + url + "}\n"
		if _, err := loadMCP(t, body); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
}

func TestMCPLiteralSecretRefusedReferencesAllowed(t *testing.T) {
	secretCases := []struct {
		name  string
		value string
	}{
		{"sk key", mcpFakeKey()},
		{"github token", mcpFakeToken()},
		{"bearer", mcpFakeBearer()},
		{"password", "pass" + "word=hunter2"},
		{"long token", "0123456789abcdef" + "0123456789abcdef"},
	}
	for _, tc := range secretCases {
		t.Run(tc.name, func(t *testing.T) {
			body := "defaults:\n  mcp:\n    servers:\n      s:\n        transport: stdio\n        command: npx\n        env:\n          KEY: \"" + tc.value + "\"\n"
			_, err := loadMCP(t, body)
			if err == nil || !strings.Contains(err.Error(), "literal secret") {
				t.Fatalf("error %v", err)
			}
			// A header value takes the same rule.
			body = "defaults:\n  mcp:\n    servers:\n      s:\n        transport: http\n        url: https://example.com/mcp\n        headers:\n          Authorization: \"" + tc.value + "\"\n"
			_, err = loadMCP(t, body)
			if err == nil || !strings.Contains(err.Error(), "literal secret") {
				t.Fatalf("header error %v", err)
			}
		})
	}

	allowed := []string{
		"${MCP_KEY}",
		"Bearer ${MCP_KEY}",
		"plain-value",
		"https://example.com/mcp",
		"ROOT=/tmp",
	}
	for _, value := range allowed {
		body := "defaults:\n  mcp:\n    servers:\n      s:\n        transport: stdio\n        command: npx\n        env:\n          KEY: \"" + value + "\"\n"
		if _, err := loadMCP(t, body); err != nil {
			t.Fatalf("%q refused: %v", value, err)
		}
	}
}

func TestLiteralSecretDetection(t *testing.T) {
	if LiteralSecret("${ENV_VAR}") || LiteralSecret("Bearer ${ENV_VAR}") || LiteralSecret("plain-value") {
		t.Fatal("a reference or plain value looked secret")
	}
	if !LiteralSecret(mcpFakeKey()) || !LiteralSecret(mcpFakeToken()) || !LiteralSecret(mcpFakeBearer()) {
		t.Fatal("a fake credential looked safe")
	}
}

func TestMCPShowAndDiffMaskValues(t *testing.T) {
	c := Builtin()
	c.MCP.MaxInlineTools = 3
	c.MCP.Servers = map[string]MCPServer{
		"fs": {
			Transport: MCPTransportStdio,
			Command:   "npx",
			Env:       map[string]string{"PLAIN": "not-a-secret", "KEY": "${FS_KEY}"},
			Headers:   map[string]string{"Authorization": "Bearer ${FS_TOKEN}"},
			Enabled:   true,
			Trust:     MCPTrustAsk,
			TimeoutMS: MCPDefaultTimeoutMS,
		},
	}
	shown, err := c.ShowYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "max_inline_tools: 3") || !strings.Contains(shown, "fs:") {
		t.Fatalf("show is missing mcp:\n%s", shown)
	}
	if strings.Contains(shown, "not-a-secret") {
		t.Fatalf("show leaked a literal value:\n%s", shown)
	}
	if !strings.Contains(shown, "KEY: ${FS_KEY}") {
		t.Fatalf("show hid a reference:\n%s", shown)
	}
	if !strings.Contains(shown, "Authorization: '***'") {
		t.Fatalf("show did not mask the mixed header:\n%s", shown)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "defaults:\n  mcp:\n    max_inline_tools: 3\n    servers:\n      fs:\n        transport: stdio\n        command: npx\n        env:\n          PLAIN: not-a-secret\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := DiffText(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "mcp.servers.fs.command") || !strings.Contains(diff, "(file)") {
		t.Fatalf("diff is missing the server change:\n%s", diff)
	}
	if strings.Contains(diff, "not-a-secret") {
		t.Fatalf("diff leaked a literal value:\n%s", diff)
	}
}

func TestUpsertMCPServerKeepsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "defaults:\n  # keep this comment\n  mcp:\n    max_inline_tools: 5   # cap comment\n    servers: {}\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	server := DefaultMCPServer()
	server.Transport = MCPTransportStdio
	server.Command = "npx"
	server.Args = []string{"-y", "@example/files"}
	server.Env = map[string]string{"KEY": "${FS_KEY}"}
	server.Headers = map[string]string{"X-Client": "konsolai"}
	server.Tools.Allow = []string{"read_*"}
	server.Private = true
	if err := UpsertMCPServer(path, "files", server, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"# keep this comment", "cap comment", "files:", "command: npx", "read_*", "private: true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("file is missing %q:\n%s", want, text)
		}
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c.MCPServer("files")
	if !ok || got.Command != "npx" || len(got.Args) != 2 || !got.Private || got.Tools.Allow[0] != "read_*" {
		t.Fatalf("reloaded server %+v", got)
	}
	if got.Env["KEY"] != "${FS_KEY}" || got.Headers["X-Client"] != "konsolai" {
		t.Fatalf("reloaded maps %+v %+v", got.Env, got.Headers)
	}

	if err := UpsertMCPServer(path, "files", MCPServer{}, true); err != nil {
		t.Fatal(err)
	}
	f, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err = f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.MCPServer("files"); ok {
		t.Fatal("the server was not removed")
	}
	if c.MCP.MaxInlineTools != 5 {
		t.Fatalf("remove changed max_inline_tools to %d", c.MCP.MaxInlineTools)
	}
}
