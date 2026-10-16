package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/safety"
)

func TestBaseRelativeTools(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, "svc")
	sib := filepath.Join(root, "sibling")
	if err := os.MkdirAll(filepath.Join(svc, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sib, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "package.json"), []byte(`{"name":"svc"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "src", "main.ts"), []byte("export const n = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sib, "note.txt"), []byte("sib\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("print(1)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g, err := safety.NewAt(root, svc)
	if err != nil {
		t.Fatal(err)
	}
	list := func(context.Context) ([]string, error) { return walkRel(root) }
	reg := NewRegistry(g, os.Stdout, list, nil, nil)

	listDir := mustTool(t, reg, "list_dir")
	act, err := listDir.Prepare(`{"path":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("list .: %v %s", err, res.Output)
	}
	if !strings.Contains(res.Output, "package.json") || !strings.Contains(res.Output, "src/") {
		t.Fatalf("list . want svc entries, got %q", res.Output)
	}
	if strings.Contains(res.Output, "app.py") || strings.Contains(res.Output, "sibling") {
		t.Fatalf("list . leaked root: %q", res.Output)
	}

	read := mustTool(t, reg, "read_file")
	act, err = read.Prepare(`{"path":"package.json"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, `"name":"svc"`) {
		t.Fatalf("read package.json: %v %q", err, res.Output)
	}

	write := mustTool(t, reg, "write_file")
	act, err = write.Prepare(`{"path":"x.txt","content":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("write x.txt: %v %s", err, res.Output)
	}
	if _, err := os.Stat(filepath.Join(svc, "x.txt")); err != nil {
		t.Fatalf("x.txt not in svc: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "x.txt")); err == nil {
		t.Fatal("x.txt written at root")
	}

	sh := mustTool(t, reg, "run_shell")
	act, err = sh.Prepare(`{"cmd":"pwd"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("pwd: %v %s", err, res.Output)
	}
	wantBase, err := filepath.EvalSymlinks(svc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, wantBase) {
		t.Fatalf("pwd want %s, got %q", wantBase, res.Output)
	}

	search := mustTool(t, reg, "search")
	act, err = search.Prepare(`{"pattern":"export const n"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, "src/main.ts") {
		t.Fatalf("search in base: %v %q", err, res.Output)
	}
	act, err = search.Prepare(`{"pattern":"print\\(1\\)"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("search print: %v %s", err, res.Output)
	}
	if strings.Contains(res.Output, "app.py") {
		t.Fatalf("default search escaped base: %q", res.Output)
	}

	act, err = read.Prepare(`{"path":"../sibling/note.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, "sib") {
		t.Fatalf("../sibling: %v %q", err, res.Output)
	}

	outside := t.TempDir()
	outFile := filepath.Join(outside, "out.txt")
	if err := os.WriteFile(outFile, []byte("out-of-root"), 0644); err != nil {
		t.Fatal(err)
	}
	act, err = read.Prepare(`{"path":` + strconv.Quote(outFile) + `}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, "out-of-root") {
		t.Fatalf("read outside: %v %q", err, res.Output)
	}
	_, err = write.Prepare(`{"path":` + strconv.Quote(filepath.Join(outside, "x.txt")) + `,"content":"nope"}`)
	if err == nil {
		t.Fatal("write outside: want deny")
	}

	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "here.txt"), []byte("here\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g2, err := safety.NewAt(root, other)
	if err != nil {
		t.Fatal(err)
	}
	reg2 := NewRegistry(g2, os.Stdout, list, nil, nil)
	read2 := mustTool(t, reg2, "read_file")
	act, err = read2.Prepare(`{"path":"here.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, "here") {
		t.Fatalf("resume other cwd: %v %q", err, res.Output)
	}
	list2 := mustTool(t, reg2, "list_dir")
	act, err = list2.Prepare(`{"path":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError || !strings.Contains(res.Output, "here.txt") {
		t.Fatalf("resume list: %v %q", err, res.Output)
	}
	if strings.Contains(res.Output, "package.json") {
		t.Fatalf("resume used old base: %q", res.Output)
	}
}
