package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SniksaX/Konsolai/internal/diff"
	"github.com/SniksaX/Konsolai/internal/docrender"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/ui"
)

type exportDocTool struct{ guard *safety.Guard }

func newExportDoc(g *safety.Guard) *exportDocTool { return &exportDocTool{guard: g} }

func (t *exportDocTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "export_doc",
			Description: "Write an HTML or PDF document to path. Arguments are only path and format; it reads the Markdown file you already wrote.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Output path relative to the current directory."},"format":{"type":"string","enum":["html","pdf"]}},"required":["path","format"]}`),
		},
	}
}

type exportDocArgs struct {
	Path   string `json:"path"`
	Format string `json:"format"`
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

func markdownSource(output string) string {
	stem := strings.TrimSuffix(output, filepath.Ext(output))
	for _, ext := range []string{".md", ".markdown"} {
		cand := stem + ext
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
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
	srcDisplay := strings.TrimSuffix(args.Path, filepath.Ext(args.Path)) + ".md"
	srcResolved := markdownSource(resolved)
	if srcResolved == "" {
		return nil, fmt.Errorf("tools: export_doc: markdown source not found (write %s first)", srcDisplay)
	}
	body, err := os.ReadFile(srcResolved)
	if err != nil {
		return nil, fmt.Errorf("tools: export_doc: read markdown: %w", err)
	}
	act := &exportDocAction{path: resolved, display: args.Path, format: format, content: string(body)}
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
	fileName := a.display
	if a.title != "" && firstHeading(a.content) == "" {
		a.content = "# " + a.title + "\n\n" + a.content
	}
	rendered, err := docrender.Render(ctx, a.format, fileName, a.content)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	a.newBytes = rendered.Bytes
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
		Meta:   fmt.Sprintf("%s · %d diagrams", formatSize(int64(len(a.newBytes))), rendered.Diagrams),
		Change: ch,
	}, nil
}

func firstHeading(md string) string {
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
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
