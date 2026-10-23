package docrender

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTMLGoldenStructure(t *testing.T) {
	src := `# Architecture

| Fn | Role |
|----|------|
| Add | plus |
`
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
	if !strings.Contains(html, `id="architecture"`) && !strings.Contains(html, `id="Architecture"`) {
		t.Fatalf("heading id missing: %s", html)
	}
	if strings.Contains(strings.ToLower(html), "<script") {
		t.Fatalf("script in plain html: %s", html)
	}
	if res.Diagrams != 0 {
		t.Fatalf("diagrams %d", res.Diagrams)
	}
}

func TestMarkMermaidFence(t *testing.T) {
	page, err := markdownHTML("T", "```mermaid\nflowchart TD\n  A-->B\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	got := markMermaidBlocks(page)
	if !strings.Contains(got, `<pre class="mermaid">`) {
		t.Fatalf("unmarked: %s", got)
	}
	if strings.Contains(got, "language-mermaid") {
		t.Fatalf("code wrapper left: %s", got)
	}
	if !strings.Contains(got, "flowchart TD") || !strings.Contains(got, "A--&gt;B") && !strings.Contains(got, "A-->B") {
		t.Fatalf("source missing: %s", got)
	}
}

func TestMermaidBundleEmbedded(t *testing.T) {
	if len(mermaidJS) < 1_000_000 {
		t.Fatalf("mermaid bundle %d bytes", len(mermaidJS))
	}
	if !strings.Contains(string(mermaidJS), `globalThis["mermaid"]`) {
		t.Fatal("bundle does not set the mermaid global")
	}
}

func TestMermaidMarkerParsesError(t *testing.T) {
	const dom = `<div id="mermaid-done" data-status="error" data-index="2">No diagram type detected matching given configuration for text: not a diagram</div>`
	status, index, message, ok := mermaidMarker(dom)
	if !ok || status != "error" || index != 2 {
		t.Fatalf("%q %d %q %v", status, index, message, ok)
	}
	if !strings.Contains(message, "No diagram type detected") {
		t.Fatalf("%q", message)
	}
}

func TestMermaidMissingChrome(t *testing.T) {
	getenv = func(string) string { return "" }
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	userHome = func() (string, error) { return t.TempDir(), nil }
	glob = func(string) ([]string, error) { return nil, nil }
	t.Cleanup(restoreChromeHooks)
	_, err := Render(context.Background(), "html", "t.md", "# T\n\n```mermaid\nA-->B\n```\n")
	if err == nil || !strings.Contains(err.Error(), "install chromium") {
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

func TestMermaidHTMLInlineSVG(t *testing.T) {
	skipWithoutChrome(t)
	src := `# Architecture

| Fn | Role |
|----|------|
| Add | plus |

` + "```mermaid\nflowchart TD\n  A[Agent] --> B[Tools]\n```\n"
	res, err := Render(context.Background(), "html", "ARCHITECTURE.md", src)
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.Bytes)
	if !strings.Contains(html, "<svg") {
		t.Fatalf("svg missing: %s", html[:min(len(html), 500)])
	}
	if strings.Contains(strings.ToLower(html), "<script") {
		t.Fatalf("script left in html")
	}
	if strings.Contains(html, "mermaid.min.js") {
		t.Fatal("mermaid script src left in html")
	}
	if !strings.Contains(html, "<title>Architecture</title>") {
		t.Fatalf("title missing: %s", html[:min(len(html), 300)])
	}
	if res.Diagrams != 1 {
		t.Fatalf("diagrams %d", res.Diagrams)
	}
	if strings.Contains(html, "SIL Open Font License") || strings.Contains(html, "@font-face") {
		t.Fatalf("font license left in html")
	}
	if os.Getenv("KONSOLAI_TEST_TRACE") == "1" {
		tr := LastRenderTrace()
		t.Logf("launch=%s page=%s run=%s dump=%s", tr.Launch, tr.Page, tr.Run, tr.Dump)
	}
}

func TestMermaidSyntaxErrorNamesDiagram(t *testing.T) {
	skipWithoutChrome(t)
	src := "# T\n\n```mermaid\nnot a diagram\n```\n"
	_, err := Render(context.Background(), "html", "t.md", src)
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "diagram 1") || !strings.Contains(msg, "No diagram type detected") {
		t.Fatalf("%q", msg)
	}
}

func TestRenderPDFIntegration(t *testing.T) {
	skipWithoutChrome(t)
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
	t.Run("pdftotext", func(t *testing.T) {
		text := pdfText(t, res.Bytes)
		if !strings.Contains(text, "Agent") && !strings.Contains(text, "Tools") {
			t.Fatalf("pdf missing diagram text: %q", text)
		}
	})
}

func TestSVGStripsFontLicense(t *testing.T) {
	raw := []byte(`<svg xmlns="http://www.w3.org/2000/svg">
<!-- generated -->
<title>chart</title>
<desc>flowchart</desc>
<metadata>SIL Open Font License</metadata>
<style>@font-face{font-family:Treemap;src:url(x)} text{font-family:Treemap}</style>
<text font-family="Treemap">A</text>
</svg>
`)
	got := inlineSVG(raw)
	if strings.Contains(got, "SIL Open Font License") {
		t.Fatalf("still has OFL: %s", got)
	}
	if strings.Contains(got, "@font-face") {
		t.Fatalf("still has @font-face: %s", got)
	}
	t.Run("pdf", func(t *testing.T) {
		skipWithoutChrome(t)
		pdf, err := printPDF(context.Background(), wrapHTML("A", got))
		if err != nil {
			t.Fatal(err)
		}
		text := pdfText(t, pdf)
		if n := strings.Count(strings.ToLower(text), "font license"); n != 0 {
			t.Fatalf("pdftotext font license count %d: %s", n, text)
		}
		if strings.Contains(text, "SIL Open Font License") {
			t.Fatalf("pdf text has OFL: %s", text)
		}
	})
}

func pdfText(t *testing.T, pdf []byte) string {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("needs pdftotext")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(p, pdf, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("pdftotext", p, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func skipWithoutChrome(t *testing.T) {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv("KONSOLAI_CHROME")); p != "" {
		if _, err := exec.LookPath(p); err == nil {
			return
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome", "brave", "brave-browser"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	if _, err := FindChrome(); err == nil {
		return
	}
	t.Skip("needs chromium")
}

func TestChromeNoiseFilterCILines(t *testing.T) {
	stderr := []byte(strings.Join([]string{
		`[99:99:ERROR:bus.cc:1] Failed to connect to the bus: Could not parse server address: Unknown address type (examples of valid types are "tcp" and on UNIX "unix")`,
		`Fontconfig warning: "/etc/fonts/fonts.conf", line 1: not well-formed`,
		`UPower: Failed to query upower`,
		`ERROR:gpu_process_host.cc] GPU process isn't usable. Goodbye.`,
	}, "\n"))
	got := chromeFail(stderr, context.DeadlineExceeded, 30*time.Second, "diagram render")
	if got != "diagram render did not finish within 30s" {
		t.Fatalf("%q", got)
	}
	low := strings.ToLower(got)
	for _, noise := range []string{"bus", "fontconfig", "upower", "gpu"} {
		if strings.Contains(low, noise) {
			t.Fatalf("noise %q leaked: %s", noise, got)
		}
	}
	cmd := exec.Command("sh", "-c", "exit 7")
	err := cmd.Run()
	got = chromeFail(stderr, err, 30*time.Second, "diagram render")
	if got != "exit status 7" {
		t.Fatalf("noise-only exit: %q", got)
	}
	got = chromeFail([]byte(string(stderr)+"\nrender failed: syntax\n"), err, 30*time.Second, "diagram render")
	if got != "exit status 7: render failed: syntax" {
		t.Fatalf("meaningful: %q", got)
	}
}

func TestRenderTimeoutScales(t *testing.T) {
	prev := RenderTimeout()
	t.Cleanup(func() { SetRenderTimeout(prev) })
	t.Setenv("KONSOLAI_TEST_TIMEOUT_SCALE", "")
	SetRenderTimeout(60 * time.Second)
	if scaledRenderTimeout() != 60*time.Second {
		t.Fatalf("%s", scaledRenderTimeout())
	}
	t.Setenv("KONSOLAI_TEST_TIMEOUT_SCALE", "5")
	if scaledRenderTimeout() != 300*time.Second {
		t.Fatalf("%s", scaledRenderTimeout())
	}
}

func TestChromeEnvDropsBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/tmp/bus")
	t.Setenv("DBUS_SESSION_BUS_PID", "1")
	for _, e := range chromeEnv() {
		if strings.HasPrefix(e, "DBUS_SESSION_BUS_") {
			t.Fatalf("kept %s", e)
		}
	}
}
