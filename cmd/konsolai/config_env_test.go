package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
)

func configFlagFixture(t *testing.T) (project, flagConfig string) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(project, ".real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "konsolai"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("KONSOLAI_CONFIG", "")
	flagConfig = filepath.Join(project, ".real", "config.yaml")
	body := "defaults:\n  database:\n    url: postgres://u:p@db.internal.example:5432/k\n  mcp:\n    servers:\n      s:\n        command: server-binary\n        args: [SECRETARG]\n        env:\n          TOKEN: SECRETENV\n        url: https://mcp.internal.example/rpc\n"
	if err := os.WriteFile(flagConfig, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Evidence scenario: the --config file is symlinked into the project root.
	if err := os.Symlink(flagConfig, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	return project, flagConfig
}

func flagTool(t *testing.T, reg *tools.Registry, name, args string) (tools.Action, error) {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %s missing", name)
	}
	return tool.Prepare(args)
}

func TestConfigFlagPathIsProtectedAndMasked(t *testing.T) {
	project, flagConfig := configFlagFixture(t)
	if got := setActiveConfigFromFlag(flagConfig); got != flagConfig {
		t.Fatalf("setActiveConfigFromFlag = %q", got)
	}
	if os.Getenv("KONSOLAI_CONFIG") != flagConfig {
		t.Fatalf("KONSOLAI_CONFIG = %q, want the --config path", os.Getenv("KONSOLAI_CONFIG"))
	}
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(g, io.Discard, nil, nil, nil)
	for name, args := range map[string]string{
		"write_file": `{"path":"config.yaml","content":"evil: true"}`,
		"edit_file":  `{"path":"config.yaml","old_str":"a","new_str":"b"}`,
		"run_shell":  `{"cmd":"sed -i s/a/b/ config.yaml"}`,
	} {
		_, err := flagTool(t, reg, name, args)
		if err == nil || !strings.Contains(err.Error(), "/set") {
			t.Fatalf("%s did not refuse the --config path with /set pointer: %v", name, err)
		}
	}
	act, err := flagTool(t, reg, "read_file", `{"path":"config.yaml"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"server-binary", "SECRETARG", "SECRETENV", "db.internal.example", "mcp.internal.example"} {
		if strings.Contains(res.Output, leak) {
			t.Fatalf("read_file leaked %q through the --config path:\n%s", leak, res.Output)
		}
	}
	if !strings.Contains(res.Output, "<set>") || !strings.Contains(res.Output, "<masked>") {
		t.Fatalf("read_file did not return the masked view:\n%s", res.Output)
	}
}

var _ = json.Marshal
