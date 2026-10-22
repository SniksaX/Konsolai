package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

func TestShellLeadingCDRewrite(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil, nil, nil).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}

	act, err := tool.Prepare(shellJSON("cd sub && pwd", ""))
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("inside: %s", res.Output)
	}
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, want) {
		t.Fatalf("pwd %q want %q", res.Output, want)
	}

	act, err = tool.Prepare(shellJSON("cd sub; pwd", ""))
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, want) {
		t.Fatalf("semicolon: err=%v out=%s", err, res.Output)
	}

	_, err = tool.Prepare(shellJSON("cd /tmp/x && pwd", ""))
	if err == nil || err.Error() != "cwd outside the project: /tmp/x" {
		t.Fatalf("outside: %v", err)
	}
	_, err = tool.Prepare(shellJSON("cd /tmp/x; pwd", ""))
	if err == nil || err.Error() != "cwd outside the project: /tmp/x" {
		t.Fatalf("outside semicolon: %v", err)
	}

	_, err = tool.Prepare(shellJSON("cd src", ""))
	if err == nil || !strings.Contains(err.Error(), "do not use cd") {
		t.Fatalf("bare cd: %v", err)
	}
	_, err = tool.Prepare(shellJSON("echo hi && cd sub && pwd", ""))
	if err == nil || !strings.Contains(err.Error(), "do not use cd") {
		t.Fatalf("later cd: %v", err)
	}
	_, err = tool.Prepare(shellJSON("cd sub || pwd", ""))
	if err == nil || !strings.Contains(err.Error(), "do not use cd") {
		t.Fatalf("or cd: %v", err)
	}
}

func TestShellMissingCwd(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil, nil, nil).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	act, err := tool.Prepare(shellJSON("pwd", "missing-dir"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatalf("run err %v", err)
	}
	if !res.IsError || !strings.HasPrefix(res.Output, "cwd does not exist:") {
		t.Fatalf("result %+v", res)
	}
	if strings.Contains(res.Output, "fork/exec") {
		t.Fatalf("misleading exec error: %s", res.Output)
	}
	want := filepath.Join(root, "missing-dir")
	if !strings.Contains(res.Output, want) && !strings.Contains(res.Output, "missing-dir") {
		t.Fatalf("path %q", res.Output)
	}
}
