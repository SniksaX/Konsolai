package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/safety"
)

func TestExportDocHTMLChange(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(g, nil, nil, nil, nil)
	tool, ok := reg.Get("export_doc")
	if !ok {
		t.Fatal("missing export_doc")
	}
	act, err := tool.Prepare(`{"path":"out.html","format":"html","title":"T","content":"<p>Hi</p>"}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != Mutating {
		t.Fatal("want Mutating")
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatal(res.Output, err)
	}
	if res.Change == nil || res.Change.ExistedBefore || res.Change.Path != "out.html" {
		t.Fatalf("change %+v", res.Change)
	}
	if !strings.Contains(res.Change.Diff, "+") {
		t.Fatalf("html diff %q", res.Change.Diff)
	}
	b, err := os.ReadFile(filepath.Join(root, "out.html"))
	if err != nil || !strings.Contains(string(b), "<p>Hi</p>") {
		t.Fatalf("%s %v", b, err)
	}
}

func TestExportDocPDFChange(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.pdf"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	act, err := tool.Prepare(`{"path":"out.pdf","format":"pdf","title":"T","content":"Hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatal(res.Output, err)
	}
	if res.Change == nil || !res.Change.ExistedBefore || string(res.Change.Before) != "old" {
		t.Fatalf("change %+v", res.Change)
	}
	if res.Change.Diff != "(binary output)" {
		t.Fatalf("diff %q", res.Change.Diff)
	}
	b, err := os.ReadFile(filepath.Join(root, "out.pdf"))
	if err != nil || !strings.HasPrefix(string(b), "%PDF") {
		t.Fatalf("pdf %q %v", b, err)
	}
}
