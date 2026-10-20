package tools

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/docrender"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestExportDocHTMLChange(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.md"), []byte("<p>Hi</p>"), 0644); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(g, nil, nil, nil, nil)
	tool, ok := reg.Get("export_doc")
	if !ok {
		t.Fatal("missing export_doc")
	}
	act, err := tool.Prepare(`{"path":"out.html","format":"html"}`)
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
	if _, err := docrender.FindChrome(); err != nil {
		t.Skip("chrome not installed")
	}
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.pdf"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.md"), []byte("Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	act, err := tool.Prepare(`{"path":"out.pdf","format":"pdf"}`)
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

func TestExportDocSpinner(t *testing.T) {
	var buf bytes.Buffer
	restore := ui.TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.md"), []byte("<p>x</p>"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	act, err := tool.Prepare(`{"path":"out.html","format":"html"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "exporting") || !strings.Contains(buf.String(), "\033[?25l") {
		t.Fatalf("spinner %q", buf.String())
	}
}

func TestExportPrepareDoesNotRender(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.md"), []byte("<p>Hi</p>"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	act, err := tool.Prepare(`{"path":"out.html","format":"html"}`)
	if err != nil {
		t.Fatal(err)
	}
	ea := act.(*exportDocAction)
	if ea.newBytes != nil {
		t.Fatal("render must happen in Run, not Prepare")
	}
}

func TestExportDocMermaidRenders(t *testing.T) {
	if _, err := docrender.FindChrome(); err != nil {
		t.Skip("chrome not installed")
	}
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	const body = "Architecture\n\n```mermaid\nflowchart TD\n  A[Agent] --> B[Tools]\n```\n\nDone.\n"
	if err := os.WriteFile(filepath.Join(root, "out.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	htmlAct, err := tool.Prepare(`{"path":"out.html","format":"html"}`)
	if err != nil {
		t.Fatal(err)
	}
	htmlRes, err := htmlAct.Run(context.Background())
	if err != nil || htmlRes.IsError {
		t.Fatal(htmlRes.Output, err)
	}
	if !strings.Contains(htmlRes.Meta, "diagrams") {
		t.Fatalf("meta %q", htmlRes.Meta)
	}
	html, err := os.ReadFile(filepath.Join(root, "out.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "<svg") {
		t.Fatalf("html missing svg: %s", html[:min(len(html), 400)])
	}
	if strings.Contains(strings.ToLower(string(html)), "<script") {
		t.Fatal("html still has a script")
	}
	pdfAct, err := tool.Prepare(`{"path":"out.pdf","format":"pdf"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdfAct.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(root, "out.pdf")
	out, err := exec.Command("pdftotext", pdf, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("pdftotext: %s %v", out, err)
	}
	if !strings.Contains(string(out), "Arch") && !strings.Contains(string(out), "Architecture") {
		t.Fatalf("pdftotext %q", out)
	}
}

func TestExportDocRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.md"), []byte("# Hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	_, err = tool.Prepare(`{"path":"out.html","format":"html","content":"` + strings.Repeat("x", 200) + `"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want unknown field, got %v", err)
	}
	_, err = tool.Prepare(`{"path":"out.html","format":"html","title":"T"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want unknown title field, got %v", err)
	}
}

func TestExportDocReadsMarkdown(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	_, err = tool.Prepare(`{"path":"out.html","format":"html"}`)
	if err == nil || !strings.Contains(err.Error(), "markdown source not found") {
		t.Fatalf("want missing markdown, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "out.markdown"), []byte("<p>via markdown ext</p>"), 0644); err != nil {
		t.Fatal(err)
	}
	act, err := tool.Prepare(`{"path":"out.html","format":"html"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatal(res.Output, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "out.html"))
	if err != nil || !strings.Contains(string(b), "via markdown ext") {
		t.Fatalf("%s %v", b, err)
	}
}
