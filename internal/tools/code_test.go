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

func codeReg(t *testing.T) (string, *Registry) {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	list := func(ctx context.Context) ([]string, error) {
		return walkRel(root)
	}
	return root, NewRegistry(g, io.Discard, list, nil, nil)
}

func walkRel(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

func TestEditFileUniqueness(t *testing.T) {
	root, reg := codeReg(t)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("foo\nbar\nfoo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("edit_file")
	_, err := tool.Prepare(`{"path":"a.go","old_str":"foo","new_str":"baz"}`)
	if err == nil || !strings.Contains(err.Error(), "matches 2 times") {
		t.Fatalf("want uniqueness error, got %v", err)
	}
	act, err := tool.Prepare(`{"path":"a.go","old_str":"foo","new_str":"baz","replace_all":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(act.Preview(), "-foo") || !strings.Contains(act.Preview(), "+baz") {
		t.Fatalf("preview %q", act.Preview())
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	b, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(b) != "baz\nbar\nbaz\n" {
		t.Fatalf("got %q", b)
	}
}

func TestReadFileRange(t *testing.T) {
	root, reg := codeReg(t)
	if err := os.WriteFile(filepath.Join(root, "n.txt"), []byte("a\nb\nc\nd\ne\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("read_file")
	act, err := tool.Prepare(`{"path":"n.txt","start_line":2,"end_line":4}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Output)
	}
	if !strings.Contains(res.Output, "n.txt (lines 2-4 of 5)") {
		t.Fatalf("%q", res.Output)
	}
	if !strings.Contains(res.Output, "     2\tb") {
		t.Fatalf("%q", res.Output)
	}
}

func TestSearchAndFindFiles(t *testing.T) {
	root, reg := codeReg(t)
	if err := os.Mkdir(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package src\nfunc Add() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "b.ts"), []byte("export const x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{0x00, 0x01, 0x02, 0xff}, 0644); err != nil {
		t.Fatal(err)
	}
	st, _ := reg.Get("search")
	act, err := st.Prepare(`{"pattern":"func Add","glob":"*.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Output)
	}
	if !strings.Contains(res.Output, "src/a.go:2:") || res.Meta != "1 matches" {
		t.Fatalf("%q meta=%q", res.Output, res.Meta)
	}
	act, err = st.Prepare(`{"pattern":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "bin.dat") {
		t.Fatalf("binary not skipped: %q", res.Output)
	}

	ft, _ := reg.Get("find_files")
	act, err = ft.Prepare(`{"glob":"*.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatal(res.Output)
	}
	if !strings.Contains(res.Output, "src/a.go") || strings.Contains(res.Output, "b.ts") {
		t.Fatalf("%q", res.Output)
	}
	act, err = ft.Prepare(`{"glob":"src/**/*.ts"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err = act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "src/b.ts") {
		t.Fatalf("%q", res.Output)
	}
}

func TestSearchCap(t *testing.T) {
	root, reg := codeReg(t)
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("hit\n")
	}
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	st, _ := reg.Get("search")
	act, err := st.Prepare(`{"pattern":"hit"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "more matches") {
		t.Fatalf("%q", res.Output)
	}
}

func TestWriteFileDiffPreview(t *testing.T) {
	root, reg := codeReg(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("write_file")
	act, err := tool.Prepare(`{"path":"f.txt","content":"new\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(act.Preview(), "-old") || !strings.Contains(act.Preview(), "+new") {
		t.Fatalf("preview %q", act.Preview())
	}
}

func TestDocsTaskRejectsSourceEdit(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(g, io.Discard, nil, nil, nil, Options{DocsTask: true})
	tool, _ := reg.Get("edit_file")
	_, err = tool.Prepare(`{"path":"a.py","old_str":"x = 1","new_str":"x = 2"}`)
	if err == nil || err.Error() != DocsSourceReject {
		t.Fatalf("py: %v", err)
	}
	if _, err := tool.Prepare(`{"path":"a.md","old_str":"# x","new_str":"# y"}`); err != nil {
		t.Fatalf("md: %v", err)
	}
	plain := NewRegistry(g, io.Discard, nil, nil, nil)
	e, _ := plain.Get("edit_file")
	if _, err := e.Prepare(`{"path":"a.py","old_str":"x = 1","new_str":"x = 2"}`); err != nil {
		t.Fatalf("non-docs: %v", err)
	}
}
