package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/ui"
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

func TestExportDocSpinner(t *testing.T) {
	var buf bytes.Buffer
	restore := ui.TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	act, err := tool.Prepare(`{"path":"out.html","format":"html","content":"<p>x</p>"}`)
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
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	act, err := tool.Prepare(`{"path":"out.html","format":"html","title":"T","content":"<p>Hi</p>"}`)
	if err != nil {
		t.Fatal(err)
	}
	ea := act.(*exportDocAction)
	if ea.newBytes != nil {
		t.Fatal("render must happen in Run, not Prepare")
	}
}

func TestExportDocMermaidRenders(t *testing.T) {
	if _, err := exec.LookPath("mmdc"); err != nil {
		t.Skip("mmdc not installed")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	const body = "Architecture\n\n```mermaid\nflowchart TD\n  A[Agent] --> B[Tools]\n```\n\nDone.\n"
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("export_doc")
	htmlAct, err := tool.Prepare(`{"path":"out.html","format":"html","title":"Arch","content":` + quoteJSON(body) + `}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := htmlAct.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(root, "out.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "<svg") {
		t.Fatalf("html missing svg: %s", html[:min(len(html), 400)])
	}
	pdfAct, err := tool.Prepare(`{"path":"out.pdf","format":"pdf","title":"Arch","content":` + quoteJSON(body) + `}`)
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
	if _, err := exec.LookPath("pdfimages"); err == nil {
		list, err := exec.Command("pdfimages", "-list", pdf).CombinedOutput()
		if err != nil {
			t.Fatalf("pdfimages: %s %v", list, err)
		}
		if !strings.Contains(string(list), "image") && !strings.Contains(string(list), "smask") && !hasPDFImageRow(string(list)) {
			t.Fatalf("pdfimages -list has no image:\n%s", list)
		}
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func hasPDFImageRow(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 && fields[0] != "page" {
			if _, err := strconv.Atoi(fields[0]); err == nil {
				return true
			}
		}
	}
	return false
}
