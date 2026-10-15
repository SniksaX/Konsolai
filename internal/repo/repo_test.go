package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootFindsGit(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if got := Root(sub); got != dir {
		t.Fatalf("got %q want %q", got, dir)
	}
}

func TestRootWithoutGit(t *testing.T) {
	dir := t.TempDir()
	if got := Root(dir); got != dir {
		t.Fatalf("got %q want %q", got, dir)
	}
}

func TestFilesGitLs(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Skip("git init:", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	files, err := Files(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, ",")
	if !strings.Contains(joined, "a.go") || !strings.Contains(joined, "sub/b.go") {
		t.Fatalf("files %v", files)
	}
}

func TestWalkSkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	nm := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(nm, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "skip.js"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	files, err := Files(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f, "node_modules") || strings.Contains(f, "skip.js") {
			t.Fatalf("should skip %q", f)
		}
	}
	if len(files) != 1 || files[0] != "keep.go" {
		t.Fatalf("files %v", files)
	}
}

func TestDetectPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"scripts":{"build":"tsc","test":"jest","lint":"eslint .","typecheck":"tsc --noEmit"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
	p := Detect(dir)
	if len(p.Kinds) != 1 || p.Kinds[0] != "node" {
		t.Fatalf("kinds %v", p.Kinds)
	}
	want := []string{"npm run build", "npm run test", "npm run lint", "npm run typecheck"}
	if strings.Join(p.Commands, ",") != strings.Join(want, ",") {
		t.Fatalf("commands %v", p.Commands)
	}
}

func TestDetectTscFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"echo"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	p := Detect(dir)
	found := false
	for _, c := range p.Commands {
		if c == "npx tsc --noEmit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("commands %v", p.Commands)
	}
}

func TestMapCapping(t *testing.T) {
	files := []string{"a.go", "b.go", "pkg/x.go", "pkg/y.go", "pkg/sub/z.go"}
	got := Map(files, 2)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %q", got)
	}
	full := Map(files, 20)
	if !strings.Contains(full, "pkg/ (3 files)") {
		t.Fatalf("%q", full)
	}
}

func TestInstructions(t *testing.T) {
	dir := t.TempDir()
	s, err := Instructions(dir)
	if err != nil || s != "" {
		t.Fatalf("%q %v", s, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err = Instructions(dir)
	if err != nil || s != "hello" {
		t.Fatalf("%q %v", s, err)
	}
}
