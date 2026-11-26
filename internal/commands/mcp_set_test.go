package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// mcpSetEnv is a config file with one comment to keep plus an empty mcp
// section, and an Env whose approval answers are chosen per test.
func mcpSetEnv(t *testing.T) (*Env, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "defaults:\n  # kept comment\n  mcp:\n    max_inline_tools: 15\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	env, _, _ := testEnv(t)
	env.ConfigPath = path
	env.Profile = config.Builtin()
	env.Confirm = fakeConf{d: ui.Approve}
	return env, path
}

func reloadMCP(t *testing.T, path string) config.Config {
	t.Helper()
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readMCPFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetMCPAddStdioAsksOnce(t *testing.T) {
	env, path := mcpSetEnv(t)
	before := readMCPFile(t, path)
	env.Confirm = fakeConf{d: ui.Reject}
	code := runSet(context.Background(), env, []string{"mcp", "add", "stdio", "files", "npx", "-y", "@example/files"})
	if code == 0 {
		t.Fatal("a declined stdio add succeeded")
	}
	if readMCPFile(t, path) != before {
		t.Fatal("a declined stdio add wrote the file")
	}
	if _, ok := env.Profile.MCPServer("files"); ok {
		t.Fatal("a declined stdio add changed the profile")
	}

	env.Confirm = fakeConf{d: ui.Approve}
	if code := runSet(context.Background(), env, []string{"mcp", "add", "stdio", "files", "npx", "-y", "@example/files"}); code != 0 {
		t.Fatalf("approved add exit %d", code)
	}
	file := readMCPFile(t, path)
	for _, want := range []string{"# kept comment", "files:", "transport: stdio", "command: npx", "- -y"} {
		if !strings.Contains(file, want) {
			t.Fatalf("file is missing %q:\n%s", want, file)
		}
	}
	c := reloadMCP(t, path)
	s, ok := c.MCPServer("files")
	if !ok || s.Command != "npx" || len(s.Args) != 2 || !s.Enabled || s.TrustMode() != config.MCPTrustAsk {
		t.Fatalf("reloaded server %+v", s)
	}
}

func TestSetMCPAddHTTP(t *testing.T) {
	env, path := mcpSetEnv(t)
	if code := runSet(context.Background(), env, []string{"mcp", "add", "http", "docs", "http://127.0.0.1:8080/mcp"}); code != 0 {
		t.Fatalf("add http exit %d", code)
	}
	c := reloadMCP(t, path)
	s, ok := c.MCPServer("docs")
	if !ok || s.Transport != config.MCPTransportHTTP || s.URL != "http://127.0.0.1:8080/mcp" {
		t.Fatalf("reloaded server %+v", s)
	}

	code := runSet(context.Background(), env, []string{"mcp", "add", "http", "remote", "http://203.0.113.9/mcp"})
	if code != 2 {
		t.Fatalf("remote plain http exit %d", code)
	}
	if _, ok := env.Profile.MCPServer("remote"); ok {
		t.Fatal("a remote plain-http server was added")
	}
}

func TestSetMCPMutations(t *testing.T) {
	env, path := mcpSetEnv(t)
	if code := runSet(context.Background(), env, []string{"mcp", "add", "stdio", "files", "npx", "-y", "@example/files"}); code != 0 {
		t.Fatalf("add exit %d", code)
	}

	if code := runSet(context.Background(), env, []string{"mcp", "disable", "files"}); code != 0 {
		t.Fatalf("disable exit %d", code)
	}
	if s, _ := reloadMCP(t, path).MCPServer("files"); s.Enabled {
		t.Fatal("disable did not stick")
	}
	if code := runSet(context.Background(), env, []string{"mcp", "enable", "files"}); code != 0 {
		t.Fatalf("enable exit %d", code)
	}
	if s, _ := reloadMCP(t, path).MCPServer("files"); !s.Enabled {
		t.Fatal("enable did not stick")
	}

	if code := runSet(context.Background(), env, []string{"mcp", "trust", "files", "read_only"}); code != 0 {
		t.Fatalf("trust exit %d", code)
	}
	if s, _ := reloadMCP(t, path).MCPServer("files"); s.TrustMode() != config.MCPTrustReadOnly {
		t.Fatalf("trust %q", s.Trust)
	}
	if code := runSet(context.Background(), env, []string{"mcp", "trust", "files", "yolo"}); code != 2 {
		t.Fatal("a bogus trust level was accepted")
	}

	if code := runSet(context.Background(), env, []string{"mcp", "allow", "files", "read_*"}); code != 0 {
		t.Fatalf("allow exit %d", code)
	}
	if code := runSet(context.Background(), env, []string{"mcp", "deny", "files", "write_*"}); code != 0 {
		t.Fatalf("deny exit %d", code)
	}
	if code := runSet(context.Background(), env, []string{"mcp", "allow", "files", "read_["}); code != 2 {
		t.Fatal("an invalid glob was accepted")
	}
	s, _ := reloadMCP(t, path).MCPServer("files")
	if len(s.Tools.Allow) != 1 || s.Tools.Allow[0] != "read_*" || s.Tools.Deny[0] != "write_*" {
		t.Fatalf("tools %+v", s.Tools)
	}

	if code := runSet(context.Background(), env, []string{"mcp", "header", "files", "Authorization", "Bearer ${MCP_TOKEN}"}); code != 0 {
		t.Fatalf("header exit %d", code)
	}
	if code := runSet(context.Background(), env, []string{"mcp", "env", "files", "ROOT", "/tmp"}); code != 0 {
		t.Fatalf("env exit %d", code)
	}
	s, _ = reloadMCP(t, path).MCPServer("files")
	if s.Headers["Authorization"] != "Bearer ${MCP_TOKEN}" || s.Env["ROOT"] != "/tmp" {
		t.Fatalf("maps %+v %+v", s.Headers, s.Env)
	}

	if code := runSet(context.Background(), env, []string{"mcp", "remove", "files"}); code != 0 {
		t.Fatalf("remove exit %d", code)
	}
	if _, ok := reloadMCP(t, path).MCPServer("files"); ok {
		t.Fatal("remove did not stick")
	}
	if !strings.Contains(readMCPFile(t, path), "# kept comment") {
		t.Fatal("remove lost the file comment")
	}
}

func TestSetMCPRefusesLiteralSecrets(t *testing.T) {
	env, path := mcpSetEnv(t)
	if code := runSet(context.Background(), env, []string{"mcp", "add", "stdio", "files", "npx"}); code != 0 {
		t.Fatalf("add exit %d", code)
	}
	key := "sk" + "-set-" + "0a1b2c3d4e5f6071"
	for _, args := range [][]string{
		{"mcp", "header", "files", "Authorization", "Bearer " + key},
		{"mcp", "env", "files", "API_KEY", key},
	} {
		if code := runSet(context.Background(), env, args); code != 2 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
	text := readMCPFile(t, path)
	if strings.Contains(text, key) {
		t.Fatal("the literal secret reached the config file")
	}
}

func TestSetMCPUsageAndUnknownServer(t *testing.T) {
	env, _ := mcpSetEnv(t)
	for _, args := range [][]string{
		{"mcp", "add", "stdio", "files"},
		{"mcp", "add", "carrier", "files", "x"},
		{"mcp", "remove", "nope"},
		{"mcp", "enable", "nope"},
		{"mcp", "trust", "nope", "auto"},
		{"mcp", "allow", "nope", "read_*"},
		{"mcp", "bogus"},
	} {
		if code := runSet(context.Background(), env, args); code != 2 {
			t.Errorf("%v exit %d", args, code)
		}
	}
	if code := runSet(context.Background(), env, []string{"mcp"}); code != 0 {
		t.Errorf("/set mcp list exit %d", code)
	}
}

func TestSetMCPSessionDoesNotWrite(t *testing.T) {
	env, path := mcpSetEnv(t)
	before := readMCPFile(t, path)
	if code := runSet(context.Background(), env, []string{"mcp", "add", "http", "docs", "https://example.com/mcp", "--session"}); code != 0 {
		t.Fatalf("session add exit %d", code)
	}
	if readMCPFile(t, path) != before {
		t.Fatal("--session wrote the file")
	}
	if s, ok := env.Profile.MCPServer("docs"); !ok || s.URL != "https://example.com/mcp" {
		t.Fatalf("session server %+v", s)
	}
}

func TestSetMCPTreeAndCompletions(t *testing.T) {
	env, _ := mcpSetEnv(t)
	setCmd := Default().Lookup("set")
	if setCmd == nil || setCmd.Tree == nil {
		t.Fatal("/set has no tree")
	}
	tree := setCmd.Tree(env)
	mcp, ok := findChoice(tree, "mcp")
	if !ok {
		t.Fatal("the /set tree has no mcp entry")
	}
	if len(mcp.Next) == 0 {
		t.Fatal("mcp has no subcommand level")
	}
	for _, want := range []string{"add", "remove", "enable", "disable", "trust", "allow", "deny", "header", "env"} {
		if _, ok := findChoice(mcp.Next, want); !ok {
			t.Errorf("mcp level is missing %s", want)
		}
	}
	add, _ := findChoice(mcp.Next, "add")
	if _, ok := findChoice(add.Next, "stdio"); !ok {
		t.Fatal("add has no stdio choice")
	}
	if _, ok := findChoice(add.Next, "http"); !ok {
		t.Fatal("add has no http choice")
	}
	if got := config.CompleteSet([]string{"mcp"}, "", true); len(got) == 0 {
		t.Fatal("no /set mcp completions")
	}
	if got := config.CompleteSet([]string{"mcp", "add"}, "", true); len(got) != 2 {
		t.Fatalf("mcp add completions %v", got)
	}
	if got := config.CompleteSet([]string{"mcp", "trust"}, "", true); len(got) != 3 {
		t.Fatalf("mcp trust completions %v", got)
	}
}
