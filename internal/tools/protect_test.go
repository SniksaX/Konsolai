package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/safety"
)

type protectEnv struct {
	project string
	guard   *safety.Guard
	reg     *Registry
}

func protectFixture(t *testing.T) protectEnv {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(project, ".real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "konsolai"), 0o700); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(project, ".real", "config.yaml")
	body := "defaults:\n  database:\n    url: postgres://u:p@db.internal.example:5432/k\n  mcp:\n    servers:\n      s:\n        transport: http\n        command: server-binary\n        args: [SECRETARG]\n        env:\n          TOKEN: SECRETENV\n        headers:\n          Authorization: SECRETHEADER\n        url: https://mcp.internal.example/rpc\n"
	if err := os.WriteFile(active, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// The evidence scenario: config.yaml is a symlink in the project root.
	if err := os.Symlink(active, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active+".bak-1", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("KONSOLAI_CONFIG", active)
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	return protectEnv{project: project, guard: g, reg: NewRegistry(g, io.Discard, nil, nil, nil)}
}

func prepare(t *testing.T, reg *Registry, name, args string) (Action, error) {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("tool %s missing", name)
	}
	return tool.Prepare(args)
}

func TestConfigSymlinkWriteIsRefusedWithSetPointer(t *testing.T) {
	fx := protectFixture(t)
	cases := map[string]string{
		"write_file": `{"path":"config.yaml","content":"evil: true"}`,
		"edit_file":  `{"path":"config.yaml","old_str":"a","new_str":"b"}`,
		"export_doc": `{"path":"config.yaml","format":"html"}`,
	}
	for name, args := range cases {
		_, err := prepare(t, fx.reg, name, args)
		if err == nil || !strings.Contains(err.Error(), "/set") {
			t.Fatalf("%s did not refuse the config symlink with /set pointer: %v", name, err)
		}
	}
	// The raw target path and the backup are protected too.
	if _, err := prepare(t, fx.reg, "write_file", `{"path":".real/config.yaml.bak-1","content":"x"}`); err == nil || !strings.Contains(err.Error(), "/set") {
		t.Fatalf("backup not protected: %v", err)
	}
}

func TestConfigWriteInsideNamedRootIsRefused(t *testing.T) {
	fx := protectFixture(t)
	outside := filepath.Join(t.TempDir(), "outside-config.yaml")
	if err := os.WriteFile(outside, []byte("defaults: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_CONFIG", outside)
	fx.guard.AllowPlanFiles([]string{outside})
	if _, err := prepare(t, fx.reg, "write_file", `{"path":`+protectQuote(outside)+`,"content":"x"}`); err == nil || !strings.Contains(err.Error(), "/set") {
		t.Fatalf("named-root config write was not refused: %v", err)
	}
}

func protectQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestReadFileReturnsMaskedConfig(t *testing.T) {
	fx := protectFixture(t)
	act, err := prepare(t, fx.reg, "read_file", `{"path":"config.yaml"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"SECRETARG", "SECRETENV", "SECRETHEADER", "db.internal.example", "mcp.internal.example", "server-binary"} {
		if strings.Contains(res.Output, leak) {
			t.Fatalf("read_file leaked %q:\n%s", leak, res.Output)
		}
	}
	if !strings.Contains(res.Output, "<set>") || !strings.Contains(res.Output, "<masked>") {
		t.Fatalf("read_file did not return the masked view:\n%s", res.Output)
	}
}

func TestShellCommandsTouchingConfigAreRefused(t *testing.T) {
	fx := protectFixture(t)
	variants := []string{
		"echo x > config.yaml", "echo x >> config.yaml", "tee -a config.yaml < x",
		"sed -i s/a/b/ config.yaml", "mv config.yaml other", "cp other config.yaml",
		"truncate -s 0 config.yaml", "dd of=config.yaml", "python3 -c 'open(\"config.yaml\",\"w\").write(\"x\")'",
		"node -e 'require(\"fs\").writeFileSync(\"config.yaml\",\"x\")'",
		"perl -pi -e s/a/b/ config.yaml", "sh -c 'echo x > config.yaml'",
		"cat config.yaml",
	}
	for _, cmd := range variants {
		args := `{"cmd":` + protectQuote(cmd) + `}`
		_, err := prepare(t, fx.reg, "run_shell", args)
		if err == nil || !strings.Contains(err.Error(), "/set") {
			t.Fatalf("shell command not refused: %s -> %v", cmd, err)
		}
	}
}

func TestNormalProjectFileStillWrites(t *testing.T) {
	fx := protectFixture(t)
	if _, err := prepare(t, fx.reg, "write_file", `{"path":"notes.txt","content":"ok"}`); err != nil {
		t.Fatalf("ordinary write refused: %v", err)
	}
	if _, err := prepare(t, fx.reg, "run_shell", `{"cmd":"go test ./..."}`); err != nil {
		t.Fatalf("ordinary shell refused: %v", err)
	}
}

var _ = errors.New
var _ = config.ProtectedRefusal
