package tools

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/siraj/konsolai/internal/diff"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/ui"
)

type exportDocTool struct{ guard *safety.Guard }

func newExportDoc(g *safety.Guard) *exportDocTool { return &exportDocTool{guard: g} }

func (t *exportDocTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "export_doc",
			Description: "Write an HTML or PDF document to path. Prefer this over write_file for rendered documents.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Output path relative to the project root."},"format":{"type":"string","enum":["html","pdf"]},"title":{"type":"string"},"content":{"type":"string","description":"Plain text or HTML body."}},"required":["path","format","content"]}`),
		},
	}
}

type exportDocArgs struct {
	Path    string `json:"path"`
	Format  string `json:"format"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type exportDocAction struct {
	path     string
	display  string
	format   string
	title    string
	content  string
	existed  bool
	oldBytes []byte
	newBytes []byte
}

func (t *exportDocTool) Prepare(rawArgs string) (Action, error) {
	var args exportDocArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode export_doc: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("tools: export_doc: path is required")
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format != "html" && format != "pdf" {
		return nil, errors.New("tools: export_doc: format must be html or pdf")
	}
	resolved, err := t.guard.ResolvePath(args.Path)
	if err != nil {
		return nil, err
	}
	act := &exportDocAction{path: resolved, display: args.Path, format: format, title: args.Title, content: args.Content}
	if b, err := os.ReadFile(resolved); err == nil {
		act.existed = true
		act.oldBytes = b
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("tools: export_doc: stat: %w", err)
	}
	return act, nil
}

func (a *exportDocAction) FilePath() string { return a.display }

func (a *exportDocAction) Summary() string {
	return fmt.Sprintf("export %s (%s)", a.display, a.format)
}

func (a *exportDocAction) Preview() string {
	if a.format == "pdf" {
		return "(binary output)"
	}
	if a.newBytes == nil {
		return a.content
	}
	return diff.Unified(a.display, string(a.oldBytes), string(a.newBytes), 3)
}

func (a *exportDocAction) Kind() ActionKind { return Mutating }

func (a *exportDocAction) Run(ctx context.Context) (Result, error) {
	stop := ui.Start("exporting")
	defer stop()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	rendered, err := renderDocument(ctx, a.format, a.title, a.content)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	a.newBytes = rendered
	if err := os.MkdirAll(filepath.Dir(a.path), 0755); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if err := atomicWriteBytes(a.path, a.newBytes); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	d := a.Preview()
	ch := &FileChange{
		Path:          a.display,
		ExistedBefore: a.existed,
		AfterHash:     fileSHA256(a.newBytes),
		Diff:          d,
	}
	if a.existed {
		ch.Before = a.oldBytes
	}
	return Result{
		Output: fmt.Sprintf("wrote %s (%d bytes)", a.display, len(a.newBytes)),
		Meta:   formatSize(int64(len(a.newBytes))),
		Change: ch,
	}, nil
}

func atomicWriteBytes(path string, data []byte) error {
	mode := os.FileMode(0644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".konsolai-export-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func renderDocument(ctx context.Context, format, title, content string) ([]byte, error) {
	parts := splitMermaid(content)
	if format == "html" {
		body, err := renderHTMLBody(ctx, parts)
		if err != nil {
			return nil, err
		}
		return []byte(renderHTML(title, body)), nil
	}
	text, images, err := renderPDFParts(ctx, parts)
	if err != nil {
		return nil, err
	}
	return buildPDF(title, text, images), nil
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
	flushText := func() {
		if len(buf) == 0 {
			return
		}
		parts = append(parts, mermaidPart{src: strings.Join(buf, "\n")})
		buf = nil
	}
	for _, line := range lines {
		if !in && isMermaidOpen(line) {
			flushText()
			in = true
			continue
		}
		if in && isFenceClose(line) {
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
		flushText()
	}
	return parts
}

func isMermaidOpen(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "```") {
		return false
	}
	lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "```")))
	fields := strings.Fields(lang)
	return len(fields) > 0 && fields[0] == "mermaid"
}

func isFenceClose(line string) bool {
	return strings.TrimSpace(line) == "```"
}

func renderHTMLBody(ctx context.Context, parts []mermaidPart) (string, error) {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		if !p.diagram {
			b.WriteString(p.src)
			continue
		}
		svg, err := runMermaid(ctx, p.src, "svg")
		if err != nil {
			b.WriteString("<pre class=\"mermaid\">")
			b.WriteString(htmlEscape(p.src))
			b.WriteString("</pre>")
			continue
		}
		b.WriteString(inlineSVG(svg))
	}
	return b.String(), nil
}

func renderPDFParts(ctx context.Context, parts []mermaidPart) (string, []pdfImage, error) {
	var text strings.Builder
	var images []pdfImage
	for _, p := range parts {
		if !p.diagram {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(stripTags(p.src))
			continue
		}
		pngBytes, err := runMermaid(ctx, p.src, "png")
		if err != nil {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(p.src)
			continue
		}
		img, err := decodePNGImage(pngBytes)
		if err != nil {
			continue
		}
		images = append(images, img)
	}
	return text.String(), images, nil
}

var lookMermaid = exec.LookPath

func runMermaid(ctx context.Context, src, ext string) ([]byte, error) {
	bin, err := lookMermaid("mmdc")
	if err != nil {
		return nil, fmt.Errorf("tools: export_doc: mmdc not found: %w", err)
	}
	dir, err := os.MkdirTemp("", "konsolai-mmd-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.mmd")
	out := filepath.Join(dir, "out."+ext)
	cfg := filepath.Join(dir, "puppeteer.json")
	if err := os.WriteFile(in, []byte(src), 0600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfg, []byte(`{"args":["--no-sandbox","--disable-gpu"]}`), 0600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "-i", in, "-o", out, "-q", "-p", cfg)
	cmd.Env = mermaidEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("tools: export_doc: mmdc: %s", msg)
	}
	return os.ReadFile(out)
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
		n := strings.ToLower(e.Name())
		if strings.Contains(n, "chrome") {
			return true
		}
	}
	return false
}

func inlineSVG(raw []byte) string {
	s := string(raw)
	if i := strings.Index(s, "<svg"); i >= 0 {
		s = s[i:]
	}
	return s
}

func renderHTML(title, body string) string {
	if title == "" {
		title = "Document"
	}
	return "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\"><title>" + htmlEscape(title) +
		"</title></head><body>\n" + body + "\n</body></html>\n"
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

type pdfImage struct {
	w, h int
	rgb  []byte
}

func decodePNGImage(raw []byte) (pdfImage, error) {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return pdfImage{}, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rgb := make([]byte, w*h*3)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			rgb[i] = byte(r >> 8)
			rgb[i+1] = byte(g >> 8)
			rgb[i+2] = byte(bl >> 8)
			i += 3
		}
	}
	return pdfImage{w: w, h: h, rgb: rgb}, nil
}

func flateBytes(p []byte) []byte {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestSpeed)
	if err != nil {
		return p
	}
	_, _ = w.Write(p)
	_ = w.Close()
	return buf.Bytes()
}

func buildPDF(title, text string, images []pdfImage) []byte {
	body := strings.TrimSpace(text)
	if title != "" {
		if body != "" {
			body = title + "\n\n" + body
		} else {
			body = title
		}
	}
	const pageW, pageH, margin, fontSize, leading = 612.0, 792.0, 72.0, 12.0, 14.0
	y := pageH - margin
	var content strings.Builder
	fmt.Fprintf(&content, "BT /F1 %.0f Tf %.0f %.0f Td\n", fontSize, margin, y)
	first := true
	for _, line := range strings.Split(body, "\n") {
		if len(line) > 90 {
			line = line[:90]
		}
		if first {
			content.WriteString("(" + pdfEscape(line) + ") Tj\n")
			first = false
		} else {
			fmt.Fprintf(&content, "0 -%.0f Td ("+pdfEscape(line)+") Tj\n", leading)
		}
		y -= leading
	}
	content.WriteString("ET\n")
	y -= 16
	maxW := pageW - 2*margin
	xobjects := ""
	var imgObjs []string
	for i, im := range images {
		if im.w <= 0 || im.h <= 0 {
			continue
		}
		drawW := maxW
		drawH := drawW * float64(im.h) / float64(im.w)
		if drawH > y-margin && y-margin > 80 {
			drawH = y - margin
			drawW = drawH * float64(im.w) / float64(im.h)
		}
		name := fmt.Sprintf("Im%d", i+1)
		fmt.Fprintf(&content, "q %.2f 0 0 %.2f %.0f %.2f cm /%s Do Q\n", drawW, drawH, margin, y-drawH, name)
		y -= drawH + 12
		comp := flateBytes(im.rgb)
		imgObjs = append(imgObjs, fmt.Sprintf(
			"<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n",
			im.w, im.h, len(comp)))
		xobjects += fmt.Sprintf("/%s %d 0 R ", name, 6+i)
	}
	stream := content.String()
	objects := [][]byte{
		[]byte("1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n"),
		[]byte("2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n"),
		[]byte(fmt.Sprintf("3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> /XObject << %s>> >> >> endobj\n", pageW, pageH, xobjects)),
		[]byte(fmt.Sprintf("4 0 obj << /Length %d >> stream\n%s\nendstream endobj\n", len(stream), stream)),
		[]byte("5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n"),
	}
	for i, header := range imgObjs {
		comp := flateBytes(images[i].rgb)
		var obj bytes.Buffer
		fmt.Fprintf(&obj, "%d 0 obj %s", 6+i, header)
		obj.Write(comp)
		obj.WriteString("\nendstream endobj\n")
		objects = append(objects, obj.Bytes())
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objects)+1)
	for i, obj := range objects {
		offs[i+1] = b.Len()
		b.Write(obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offs[i])
	}
	fmt.Fprintf(&b, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func pdfEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
