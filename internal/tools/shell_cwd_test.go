package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

func TestShellCwdConfinement(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil, nil, nil).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	cmd := "rm -rf /tmp/x && mkdir /tmp/x"
	_, err = tool.Prepare(shellJSON(cmd, ""))
	if err == nil || !strings.Contains(err.Error(), "denied by rule escape: /tmp/x") {
		t.Fatalf("no cwd: %v", err)
	}
	_, err = tool.Prepare(shellJSON(cmd, "/tmp/x"))
	if err == nil || err.Error() != "cwd outside the project: /tmp/x" {
		t.Fatalf("outside: %v", err)
	}
	_, err = tool.Prepare(shellJSON("pwd", ".."))
	if err == nil || err.Error() != "cwd outside the project: .." {
		t.Fatalf("dotdot: %v", err)
	}
	_, err = tool.Prepare(shellJSON("pwd", "sub/../../.."))
	if err == nil || err.Error() != "cwd outside the project: sub/../../.." {
		t.Fatalf("nested dotdot: %v", err)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink("/tmp", link); err != nil {
		t.Fatal(err)
	}
	_, err = tool.Prepare(shellJSON("pwd", "escape"))
	if err == nil || err.Error() != "cwd outside the project: escape" {
		t.Fatalf("symlink: %v", err)
	}

	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	act, err := tool.Prepare(shellJSON("pwd", "sub"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("relative cwd: %s", res.Output)
	}
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, want) {
		t.Fatalf("pwd %q want %q", res.Output, want)
	}
}

func shellJSON(cmd, cwd string) string {
	if cwd == "" {
		b, _ := json.Marshal(map[string]string{"cmd": cmd})
		return string(b)
	}
	b, _ := json.Marshal(map[string]string{"cmd": cmd, "cwd": cwd})
	return string(b)
}
