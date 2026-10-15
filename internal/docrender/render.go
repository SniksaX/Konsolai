package docrender

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

const (
	mmdcTimeout   = 60 * time.Second
	chromeTimeout = 90 * time.Second
	stderrCap     = 4 << 10
)

type Result struct {
	Bytes    []byte
	Diagrams int
}

var lookMmdc = exec.LookPath

func Render(ctx context.Context, format, fileName, content string) (Result, error) {
	expanded, n, err := expandMermaid(ctx, content)
	if err != nil {
		return Result{}, err
	}
	title := firstH1(content)
	if title == "" {
		title = filepath.Base(fileName)
		if title == "" || title == "." {
			title = "Document"
		}
	}
	page, err := markdownHTML(title, expanded)
	if err != nil {
		return Result{}, err
	}
	if format == "html" {
		return Result{Bytes: []byte(page), Diagrams: n}, nil
	}
	pdf, err := printPDF(ctx, page)
	if err != nil {
		return Result{}, err
	}
	return Result{Bytes: pdf, Diagrams: n}, nil
}

func expandMermaid(ctx context.Context, content string) (string, int, error) {
	parts := splitMermaid(content)
	n := 0
	for _, p := range parts {
		if p.diagram {
			n++
		}
	}
	if n == 0 {
		return content, 0, nil
	}
	if _, err := lookMmdc("mmdc"); err != nil {
		return "", 0, errors.New("mmdc not found; install with: npm i -g @mermaid-js/mermaid-cli")
	}
	dir, err := os.MkdirTemp("", "konsolai-mmd-")
	if err != nil {
		return "", 0, fmt.Errorf("docrender: mermaid temp: %w", err)
	}
	defer os.RemoveAll(dir)
	var b strings.Builder
	di := 0
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		if !p.diagram {
			b.WriteString(p.src)
			continue
		}
		svg, err := renderMermaid(ctx, dir, di, p.src)
		if err != nil {
			return "", 0, err
		}
		b.WriteString(inlineSVG(svg))
		di++
	}
	return b.String(), n, nil
}

type mermaidPart struct {
	src     string
	diagram bool
}

func splitMermaid(content string) []mermaidPart {
	lines := strings.Split(content, "\n")
	var parts []mermaidPart
	var buf []string
	in := false
	flush := func() {
		if len(buf) == 0 {
			return
		}
		parts = append(parts, mermaidPart{src: strings.Join(buf, "\n")})
		buf = nil
	}
	for _, line := range lines {
		if !in && isMermaidOpen(line) {
			flush()
			in = true
			continue
		}
		if in && strings.TrimSpace(line) == "```" {
			parts = append(parts, mermaidPart{src: strings.Join(buf, "\n"), diagram: true})
			buf = nil
			in = false
			continue
		}
		buf = append(buf, line)
	}
	if in {
		parts = append(parts, mermaidPart{src: strings.Join(buf, "\n"), diagram: true})
	} else {
		flush()
	}
	return parts
}

func isMermaidOpen(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "```") {
		return false
	}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "```"))))
	return len(fields) > 0 && fields[0] == "mermaid"
}

func renderMermaid(ctx context.Context, dir string, index int, src string) ([]byte, error) {
	in := filepath.Join(dir, fmt.Sprintf("d%d.mmd", index))
	out := filepath.Join(dir, fmt.Sprintf("d%d.svg", index))
	cfg := filepath.Join(dir, "puppeteer.json")
	if err := os.WriteFile(in, []byte(src), 0600); err != nil {
		return nil, fmt.Errorf("docrender: mermaid write: %w", err)
	}
	if err := os.WriteFile(cfg, []byte(`{"args":["--no-sandbox","--disable-gpu"]}`), 0600); err != nil {
		return nil, fmt.Errorf("docrender: mermaid write: %w", err)
	}
	bin, err := lookMmdc("mmdc")
	if err != nil {
		return nil, errors.New("mmdc not found; install with: npm i -g @mermaid-js/mermaid-cli")
	}
	_, stderr, err := runGrouped(ctx, mmdcTimeout, dir, mermaidEnv(), true, bin, "-i", in, "-o", out, "-b", "transparent", "-p", cfg)
	if err != nil {
		return nil, fmt.Errorf("docrender: mermaid diagram %d: %s", index+1, tailErr(stderr, err))
	}
	svg, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("docrender: mermaid diagram %d: %w", index+1, err)
	}
	return svg, nil
}

var (
	reSVGComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reSVGMeta     = regexp.MustCompile(`(?is)<metadata\b[^>]*>.*?</metadata>`)
	reSVGTitle    = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title>`)
	reSVGDesc     = regexp.MustCompile(`(?is)<desc\b[^>]*>.*?</desc>`)
	reSVGStyle    = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reSVGFontFam  = regexp.MustCompile(`font-family\s*:\s*[^;}"']+`)
	svgFontFamily = `font-family:Inter,system-ui,-apple-system,"Segoe UI",sans-serif`
)

func inlineSVG(raw []byte) string {
	s := string(raw)
	if i := strings.Index(s, "<svg"); i >= 0 {
		s = s[i:]
	}
	s = reSVGComment.ReplaceAllString(s, "")
	s = reSVGMeta.ReplaceAllString(s, "")
	s = reSVGTitle.ReplaceAllString(s, "")
	s = reSVGDesc.ReplaceAllString(s, "")
	s = reSVGStyle.ReplaceAllStringFunc(s, func(block string) string {
		if strings.Contains(strings.ToLower(block), "@font-face") {
			return ""
		}
		return reSVGFontFam.ReplaceAllString(block, svgFontFamily)
	})
	s = reSVGFontFam.ReplaceAllString(s, svgFontFamily)
	return s
}

func firstH1(md string) string {
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func markdownHTML(title, src string) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	var body bytes.Buffer
	if err := md.Convert([]byte(src), &body); err != nil {
		return "", fmt.Errorf("docrender: markdown: %w", err)
	}
	return wrapHTML(title, body.String()), nil
}

const docCSS = `html{font-family:Inter,system-ui,-apple-system,"Segoe UI",sans-serif;font-size:11pt}
body{max-width:780px;margin:0 auto;padding:1.5rem;line-height:1.5;color:#111}
h1,h2,h3,h4{margin:1.4em 0 .6em;line-height:1.25}
h1{font-size:1.8em}h2{font-size:1.4em}h3{font-size:1.2em}
pre{background:#f3f4f6;padding:.8em 1em;overflow-x:auto;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.9em}
code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
pre code{background:none;padding:0}
table{border-collapse:collapse;width:100%;margin:1em 0}
th,td{border:1px solid #d1d5db;padding:.4em .7em;text-align:left}
th{background:#f3f4f6}
svg{max-width:100%;height:auto;display:block;margin:1em auto}
@page{size:A4;margin:18mm}
@media print{pre,table,svg{break-inside:avoid;page-break-inside:avoid}}`

func wrapHTML(title, body string) string {
	return "<!DOCTYPE html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>" +
		htmlEscape(title) + "</title>\n<style>\n" + docCSS + "\n</style></head><body>\n" +
		body + "\n</body></html>\n"
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

func printPDF(ctx context.Context, page string) ([]byte, error) {
	bin, err := FindChrome()
	if err != nil {
		return nil, fmt.Errorf("docrender: %w", err)
	}
	dir, err := os.MkdirTemp("", "konsolai-pdf-")
	if err != nil {
		return nil, fmt.Errorf("docrender: pdf temp: %w", err)
	}
	defer os.RemoveAll(dir)
	htmlPath := filepath.Join(dir, "doc.html")
	pdfPath := filepath.Join(dir, "doc.pdf")
	profile := filepath.Join(dir, "user-data")
	if err := os.WriteFile(htmlPath, []byte(page), 0600); err != nil {
		return nil, fmt.Errorf("docrender: pdf write: %w", err)
	}
	if err := os.Mkdir(profile, 0700); err != nil {
		return nil, fmt.Errorf("docrender: pdf profile: %w", err)
	}
	abs, err := filepath.Abs(htmlPath)
	if err != nil {
		return nil, fmt.Errorf("docrender: pdf path: %w", err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: abs}).String()
	_, stderr, err := runGrouped(ctx, chromeTimeout, dir, chromeEnv(), true, bin,
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-extensions",
		"--ozone-platform=headless",
		"--no-pdf-header-footer",
		"--user-data-dir="+profile,
		"--print-to-pdf="+pdfPath,
		fileURL,
	)
	if err != nil {
		return nil, fmt.Errorf("docrender: chrome: %s", tailErr(stderr, err))
	}
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("docrender: chrome: %s", tailErr(stderr, err))
	}
	return pdf, nil
}

func chromeEnv() []string {
	skip := map[string]bool{"WAYLAND_DISPLAY": true, "DISPLAY": true, "WAYLAND_SOCKET": true}
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if skip[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

func runGrouped(ctx context.Context, timeout time.Duration, dir string, env []string, setpgid bool, name string, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	attr := &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if setpgid {
		attr.Setpgid = true
	}
	cmd.SysProcAttr = attr
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return nil, errb.Bytes(), err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return outb.Bytes(), errb.Bytes(), err
	case <-ctx.Done():
		if cmd.Process != nil {
			if setpgid {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
		}
		<-done
		return outb.Bytes(), errb.Bytes(), ctx.Err()
	}
}

func tailErr(stderr []byte, err error) string {
	s := strings.TrimSpace(string(stderr))
	if len(stderr) > stderrCap {
		s = strings.TrimSpace(string(stderr[len(stderr)-stderrCap:]))
	}
	if s == "" {
		return err.Error()
	}
	return s
}

func mermaidEnv() []string {
	cache := os.Getenv("PUPPETEER_CACHE_DIR")
	if cache == "" || !puppeteerHasChrome(cache) {
		if home, err := os.UserHomeDir(); err == nil {
			fallback := filepath.Join(home, ".cache", "puppeteer")
			if puppeteerHasChrome(fallback) {
				cache = fallback
			}
		}
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "PUPPETEER_CACHE_DIR=") {
			continue
		}
		out = append(out, e)
	}
	if cache != "" {
		out = append(out, "PUPPETEER_CACHE_DIR="+cache)
	}
	return out
}

func puppeteerHasChrome(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name()), "chrome") {
			return true
		}
	}
	return false
}
