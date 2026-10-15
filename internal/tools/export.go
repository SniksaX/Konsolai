package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/siraj/konsolai/internal/diff"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
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
	if format == "html" {
		act.newBytes = []byte(renderHTML(args.Title, args.Content))
	} else {
		act.newBytes = renderPDF(args.Title, args.Content)
	}
	return act, nil
}

func (a *exportDocAction) Summary() string {
	return fmt.Sprintf("export %s (%s, %d B)", a.display, a.format, len(a.newBytes))
}

func (a *exportDocAction) Preview() string {
	if a.format == "pdf" {
		return "(binary output)"
	}
	return diff.Unified(a.display, string(a.oldBytes), string(a.newBytes), 3)
}

func (a *exportDocAction) Kind() ActionKind { return Mutating }

func (a *exportDocAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0755); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if err := atomicWrite(a.path, string(a.newBytes)); err != nil {
		if a.format == "pdf" {
			if err := os.WriteFile(a.path, a.newBytes, 0644); err != nil {
				return Result{Output: err.Error(), IsError: true}, nil
			}
		} else {
			return Result{Output: err.Error(), IsError: true}, nil
		}
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

func renderPDF(title, body string) []byte {
	text := body
	if title != "" {
		text = title + "\n\n" + body
	}
	escaped := pdfEscape(text)
	stream := "BT /F1 12 Tf 72 720 Td (" + escaped + ") Tj ET"
	objects := []string{
		"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n",
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n",
		"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj\n",
		fmt.Sprintf("4 0 obj << /Length %d >> stream\n%s\nendstream endobj\n", len(stream), stream),
		"5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.1\n")
	offs := make([]int, len(objects)+1)
	for i, obj := range objects {
		offs[i+1] = b.Len()
		b.WriteString(obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offs[i])
	}
	fmt.Fprintf(&b, "trailer << /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(b.String())
}

func pdfEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}
