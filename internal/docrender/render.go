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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

const (
	chromeTimeout   = 90 * time.Second
	defaultRenderTO = 60 * time.Second
	stderrCap       = 4 << 10
)

var (
	renderMu      sync.Mutex
	renderTimeout = defaultRenderTO
	lastTrace     RenderTrace
)

// RenderTrace is one Mermaid Chrome run, filled when a dump finishes.
type RenderTrace struct {
	Launch time.Duration
	Page   time.Duration
	Run    time.Duration
	Dump   time.Duration
}

// SetRenderTimeout sets the Mermaid Chrome wait. Non-positive values restore the default.
func SetRenderTimeout(d time.Duration) {
	if d <= 0 {
		d = defaultRenderTO
	}
	renderMu.Lock()
	renderTimeout = d
	renderMu.Unlock()
}

// RenderTimeout is the configured Mermaid wait before the test scale.
func RenderTimeout() time.Duration {
	renderMu.Lock()
	defer renderMu.Unlock()
	if renderTimeout <= 0 {
		return defaultRenderTO
	}
	return renderTimeout
}

// LastRenderTrace returns timings from the latest Mermaid dump.
func LastRenderTrace() RenderTrace {
	renderMu.Lock()
	defer renderMu.Unlock()
	return lastTrace
}

func scaledRenderTimeout() time.Duration {
	d := RenderTimeout()
	n := 1
	if s := os.Getenv("KONSOLAI_TEST_TIMEOUT_SCALE"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 1 {
			n = v
		}
	}
	return d * time.Duration(n)
}

type Result struct {
	Bytes    []byte
	Diagrams int
}

func Render(ctx context.Context, format, fileName, content string) (Result, error) {
	n := countMermaid(content)
	title := firstH1(content)
	if title == "" {
		title = filepath.Base(fileName)
		if title == "" || title == "." {
			title = "Document"
		}
	}
	page, err := markdownHTML(title, content)
	if err != nil {
		return Result{}, err
	}
	if n > 0 {
		page, err = applyMermaid(ctx, page)
		if err != nil {
			return Result{}, err
		}
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

func countMermaid(content string) int {
	n := 0
	for _, p := range splitMermaid(content) {
		if p.diagram {
			n++
		}
	}
	return n
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
	pageURL, err := fileURL(htmlPath)
	if err != nil {
		return nil, fmt.Errorf("docrender: pdf path: %w", err)
	}
	args := append(headlessArgs(profile),
		"--no-pdf-header-footer",
		"--print-to-pdf="+pdfPath,
		pageURL,
	)
	_, stderr, err := runGrouped(ctx, chromeTimeout, dir, chromeEnv(), true, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("docrender: chrome: %s", chromeFail(stderr, err, chromeTimeout, "pdf render"))
	}
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("docrender: chrome: %s", chromeFail(stderr, err, chromeTimeout, "pdf render"))
	}
	return pdf, nil
}

func chromeEnv() []string {
	skip := map[string]bool{
		"WAYLAND_DISPLAY":          true,
		"DISPLAY":                  true,
		"WAYLAND_SOCKET":           true,
		"DBUS_SESSION_BUS_ADDRESS": true,
		"DBUS_SESSION_BUS_PID":     true,
	}
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
	stdout, stderr, _, err = runGroupedClock(ctx, timeout, dir, env, setpgid, name, args...)
	return stdout, stderr, err
}

func runGroupedClock(ctx context.Context, timeout time.Duration, dir string, env []string, setpgid bool, name string, args ...string) (stdout, stderr []byte, launch time.Duration, err error) {
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
	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, errb.Bytes(), time.Since(t0), err
	}
	launch = time.Since(t0)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return outb.Bytes(), errb.Bytes(), launch, err
	case <-ctx.Done():
		if cmd.Process != nil {
			if setpgid {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			} else {
				_ = cmd.Process.Kill()
			}
		}
		<-done
		return outb.Bytes(), errb.Bytes(), launch, ctx.Err()
	}
}

func fileURL(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: abs}).String(), nil
}

func chromeFail(stderr []byte, err error, timeout time.Duration, what string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		sec := int(timeout.Round(time.Second) / time.Second)
		if sec < 1 {
			sec = 1
		}
		return fmt.Sprintf("%s did not finish within %ds", what, sec)
	}
	lines := meaningfulStderr(stderr)
	if code, ok := exitCode(err); ok {
		if len(lines) > 0 {
			return fmt.Sprintf("exit status %d: %s", code, lines[len(lines)-1])
		}
		return fmt.Sprintf("exit status %d", code)
	}
	if len(lines) > 0 {
		return lines[len(lines)-1]
	}
	if err != nil {
		return err.Error()
	}
	return "chrome failed"
}

func meaningfulStderr(stderr []byte) []string {
	s := string(stderr)
	if len(stderr) > stderrCap {
		s = string(stderr[len(stderr)-stderrCap:])
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || chromeNoise(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func chromeNoise(line string) bool {
	l := strings.ToLower(line)
	for _, n := range []string{
		"dbus",
		"the bus",
		"could not parse server address",
		"upower",
		"fontconfig",
		"gpu",
	} {
		if strings.Contains(l, n) {
			return true
		}
	}
	return false
}

func exitCode(err error) (int, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}

func noteRenderTrace(launch, total time.Duration, dom string) {
	page := attrMS(dom, "data-load-ms")
	run := attrMS(dom, "data-run-ms")
	dump := total - launch - page - run
	if dump < 0 {
		dump = 0
	}
	tr := RenderTrace{Launch: launch, Page: page, Run: run, Dump: dump}
	renderMu.Lock()
	lastTrace = tr
	renderMu.Unlock()
	if os.Getenv("KONSOLAI_TEST_TRACE") == "1" {
		fmt.Fprintf(os.Stderr, "render launch=%s page=%s run=%s dump=%s\n", tr.Launch, tr.Page, tr.Run, tr.Dump)
	}
}

func attrMS(dom, name string) time.Duration {
	re := regexp.MustCompile(name + `="(\d+)"`)
	m := re.FindStringSubmatch(dom)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}
