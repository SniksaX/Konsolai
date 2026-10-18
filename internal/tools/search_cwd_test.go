package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

// TestSearchFromSubdir reproduces search missing repo files when cwd is a
// subdirectory: FileLister paths are repo-relative, but the path argument was
// compared raw (so an absolute prefix never matched) and do-mode listed from
// cwd instead of the repo root.
func TestSearchFromSubdir(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	src := "package tools\n\nfunc newShell() {}\nconst name = \"run_shell\"\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "tools", "shell.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "x_test.go"), []byte("package tests\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(filepath.Join(root, "tests"))

	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	list := func(context.Context) ([]string, error) { return walkRel(root) }
	reg := NewRegistry(g, os.Stdout, list, nil, nil)

	absTools := filepath.Join(root, "internal", "tools")
	absFile := filepath.Join(absTools, "shell.go")

	search := mustTool(t, reg, "search")
	for _, args := range []map[string]string{
		{"pattern": "run_shell"},
		{"pattern": "run_shell", "path": "internal/tools"},
		{"pattern": "run_shell", "path": absTools},
		{"pattern": "run_shell", "path": absFile},
	} {
		b, _ := json.Marshal(args)
		act, err := search.Prepare(string(b))
		if err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		res, err := act.Run(context.Background())
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %s", b, err, res.Output)
		}
		if !strings.Contains(res.Output, "internal/tools/shell.go") {
			t.Fatalf("%s: want hit, got %q", b, res.Output)
		}
	}

	find := mustTool(t, reg, "find_files")
	for _, args := range []map[string]string{
		{"glob": "shell.go"},
		{"glob": "shell.go", "path": "internal/tools"},
		{"glob": "shell.go", "path": absTools},
	} {
		b, _ := json.Marshal(args)
		act, err := find.Prepare(string(b))
		if err != nil {
			t.Fatalf("find %s: %v", b, err)
		}
		res, err := act.Run(context.Background())
		if err != nil || res.IsError || !strings.Contains(res.Output, "internal/tools/shell.go") {
			t.Fatalf("find %s: %v %q", b, err, res.Output)
		}
	}

	read := mustTool(t, reg, "read_file")
	for _, p := range []string{"internal/tools/shell.go", absFile} {
		act, err := read.Prepare(`{"path":` + jsonQuote(p) + `}`)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		res, err := act.Run(context.Background())
		if err != nil || res.IsError || !strings.Contains(res.Output, "run_shell") {
			t.Fatalf("read %s: %v %q", p, err, res.Output)
		}
	}

	listDir := mustTool(t, reg, "list_dir")
	for _, p := range []string{"internal/tools", absTools} {
		act, err := listDir.Prepare(`{"path":` + jsonQuote(p) + `}`)
		if err != nil {
			t.Fatalf("list %s: %v", p, err)
		}
		res, err := act.Run(context.Background())
		if err != nil || res.IsError || !strings.Contains(res.Output, "shell.go") {
			t.Fatalf("list %s: %v %q", p, err, res.Output)
		}
	}
}

func mustTool(t *testing.T, reg *Registry, name string) Tool {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("missing %s", name)
	}
	return tool
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
