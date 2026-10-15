package docrender

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLGoldenStructure(t *testing.T) {
	bin := fakeMmdc(t, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; fi
  shift
done
printf '%s\n' '<svg xmlns="http://www.w3.org/2000/svg" id="diagram"></svg>' > "$out"
`)
	lookMmdc = func(string) (string, error) { return bin, nil }
	t.Cleanup(func() { lookMmdc = exec.LookPath })

	src := `# Architecture

| Fn | Role |
|----|------|
| Add | plus |

` + "```mermaid\nflowchart TD\n  A-->B\n```\n"
	res, err := Render(context.Background(), "html", "ARCHITECTURE.md", src)
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.Bytes)
	if !strings.Contains(html, "<title>Architecture</title>") {
		t.Fatalf("title: %s", html[:min(len(html), 400)])
	}
	if !strings.Contains(html, "<table") || !strings.Contains(html, "<th") {
		t.Fatalf("table missing: %s", html)
	}
	if !strings.Contains(html, "<svg") || !strings.Contains(html, `id="diagram"`) {
		t.Fatalf("svg missing: %s", html)
	}
	if !strings.Contains(html, `id="architecture"`) && !strings.Contains(html, `id="Architecture"`) {
		t.Fatalf("heading id missing: %s", html)
	}
	if res.Diagrams != 1 {
		t.Fatalf("diagrams %d", res.Diagrams)
	}
}

func TestMermaidErrorPropagatesIndexAndStderr(t *testing.T) {
	bin := fakeMmdc(t, `#!/bin/sh
echo "parse error at line 2: bad node" >&2
exit 1
`)
	lookMmdc = func(string) (string, error) { return bin, nil }
	t.Cleanup(func() { lookMmdc = exec.LookPath })
	src := "# T\n\n```mermaid\nnot a diagram\n```\n"
	_, err := Render(context.Background(), "html", "t.md", src)
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "diagram 1") || !strings.Contains(msg, "parse error at line 2") {
		t.Fatalf("%q", msg)
	}
}

func TestMermaidMissingListsInstall(t *testing.T) {
	lookMmdc = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookMmdc = exec.LookPath })
	_, err := Render(context.Background(), "html", "t.md", "# T\n\n```mermaid\nA-->B\n```\n")
	if err == nil || !strings.Contains(err.Error(), "npm i -g @mermaid-js/mermaid-cli") {
		t.Fatalf("%v", err)
	}
}

func TestTitleFallsBackToFileName(t *testing.T) {
	res, err := Render(context.Background(), "html", "notes.md", "just text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Bytes), "<title>notes.md</title>") {
		t.Fatalf("%s", res.Bytes)
	}
}

func TestRenderPDFIntegration(t *testing.T) {
	if _, err := lookMmdc("mmdc"); err != nil {
		t.Skip("mmdc not installed")
	}
	if _, err := FindChrome(); err != nil {
		t.Skip("chrome not installed")
	}
	src := `# Architecture

| Fn | Role |
|----|------|
| Add | plus |

` + "```mermaid\nflowchart TD\n  A[Agent] --> B[Tools]\n```\n"
	res, err := Render(context.Background(), "pdf", "ARCHITECTURE.md", src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(res.Bytes), "%PDF") {
		t.Fatalf("not pdf %q", res.Bytes[:min(len(res.Bytes), 20)])
	}
	if res.Diagrams != 1 {
		t.Fatalf("diagrams %d", res.Diagrams)
	}
}

func TestSVGStripsFontLicense(t *testing.T) {
	bin := fakeMmdc(t, `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; fi
  shift
done
cat > "$out" <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg">
<!-- generated -->
<title>chart</title>
<desc>flowchart</desc>
<metadata>SIL Open Font License</metadata>
<style>@font-face{font-family:Treemap;src:url(x)} text{font-family:Treemap}</style>
<text font-family="Treemap">A</text>
</svg>
SVG
`)
	lookMmdc = func(string) (string, error) { return bin, nil }
	t.Cleanup(func() { lookMmdc = exec.LookPath })

	src := "# Doc\n\n```mermaid\nflowchart TD\n  A-->B\n```\n"
	res, err := Render(context.Background(), "html", "A.md", src)
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.Bytes)
	if strings.Contains(html, "SIL Open Font License") {
		t.Fatalf("html still has OFL: %s", html)
	}
	if strings.Contains(html, "@font-face") {
		t.Fatalf("html still has @font-face: %s", html)
	}

	if _, err := FindChrome(); err != nil {
		return
	}
	pdf, err := Render(context.Background(), "pdf", "A.md", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "A.pdf")
	if err := os.WriteFile(p, pdf.Bytes, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("pdftotext", p, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.ToLower(string(out)), "font license"); n != 0 {
		t.Fatalf("pdftotext font license count %d: %s", n, out)
	}
	if strings.Contains(string(out), "SIL Open Font License") {
		t.Fatalf("pdf text has OFL: %s", out)
	}
}

func fakeMmdc(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mmdc")
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}
