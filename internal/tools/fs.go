package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/diff"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/redact"
	"github.com/siraj/konsolai/internal/safety"
)

const (
	readFileCap  = 32 * 1024
	readLineCap  = 2000
	listDirCap   = 200
	previewLines = 20
)

type writeFileTool struct{ guard *safety.Guard }

func newWriteFile(g *safety.Guard) *writeFileTool { return &writeFileTool{guard: g} }

func (t *writeFileTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "write_file",
			Description: "Create or overwrite a UTF-8 text file at path relative to the current directory. Parent directories are created. Never use shell heredocs or echo redirection for file contents.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the current directory. Use only for new files; prefer edit_file for existing files."},"content":{"type":"string","description":"Full file contents to write."}},"required":["path","content"]}`),
		},
	}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeFileAction struct {
	path      string
	display   string
	content   string
	oldText   string
	overwrite bool
}

func (t *writeFileTool) Prepare(rawArgs string) (Action, error) {
	var args writeFileArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode write_file: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("tools: write_file: path is required")
	}
	resolved, err := t.guard.ResolvePath(args.Path)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Lstat(resolved)
	overwrite := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("tools: write_file: stat: %w", statErr)
	}
	old := ""
	if overwrite {
		b, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("tools: write_file: read: %w", err)
		}
		old = string(b)
	}
	return &writeFileAction{path: resolved, display: args.Path, content: args.Content, oldText: old, overwrite: overwrite}, nil
}

func (a *writeFileAction) FilePath() string { return a.display }

func (a *writeFileAction) Summary() string {
	kind := "new"
	if a.overwrite {
		kind = "overwrite"
	}
	return fmt.Sprintf("write %s (%d B, %s)", a.display, len(a.content), kind)
}

func (a *writeFileAction) Preview() string {
	if a.overwrite {
		return diff.Unified(a.display, a.oldText, a.content, 3)
	}
	return firstLines(a.content, previewLines)
}

func (a *writeFileAction) Kind() ActionKind { return Mutating }

func (a *writeFileAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0755); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if err := atomicWrite(a.path, a.content); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	meta := fmt.Sprintf("%d B", len(a.content))
	if a.overwrite {
		add, rem := diff.Stat(a.oldText, a.content)
		meta = fmt.Sprintf("+%d -%d", add, rem)
	}
	ch := &FileChange{
		Path:          a.display,
		ExistedBefore: a.overwrite,
		AfterHash:     fileSHA256([]byte(a.content)),
		Diff:          diff.Unified(a.display, a.oldText, a.content, 3),
	}
	if a.overwrite {
		ch.Before = []byte(a.oldText)
	}
	return Result{
		Output: fmt.Sprintf("wrote %s (%d bytes)", a.display, len(a.content)),
		Meta:   meta,
		Change: ch,
	}, nil
}

func fileSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func atomicWrite(path, content string) error {
	mode := os.FileMode(0644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".konsolai-write-*")
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
	if _, err := io.WriteString(f, content); err != nil {
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

type readFileTool struct{ guard *safety.Guard }

func newReadFile(g *safety.Guard) *readFileTool { return &readFileTool{guard: g} }

func (t *readFileTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "read_file",
			Description: "Read a UTF-8 text file. Optional 1-based start_line and end_line (inclusive). Output is line-numbered and capped at 2000 lines or 32 KiB.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer","description":"1-based inclusive start. Default 1."},"end_line":{"type":"integer","description":"1-based inclusive end. Default last line."}},"required":["path"]}`),
		},
	}
}

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type readFileAction struct {
	path      string
	display   string
	startLine int
	endLine   int
}

func (t *readFileTool) Prepare(rawArgs string) (Action, error) {
	var args readFileArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode read_file: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("tools: read_file: path is required")
	}
	resolved, err := t.guard.ResolvePath(args.Path)
	if err != nil {
		return nil, err
	}
	return &readFileAction{path: resolved, display: args.Path, startLine: args.StartLine, endLine: args.EndLine}, nil
}

func (a *readFileAction) Summary() string  { return "read " + a.display }
func (a *readFileAction) Preview() string  { return "" }
func (a *readFileAction) Kind() ActionKind { return ReadOnly }

func (a *readFileAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	fi, err := os.Stat(a.path)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if fi.IsDir() {
		return Result{Output: "error: is a directory", IsError: true, Meta: "is a directory"}, nil
	}
	allBytes, err := os.ReadFile(a.path)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if !utf8.Valid(allBytes) {
		return Result{Output: "error: binary content (not UTF-8)", IsError: true}, nil
	}
	text := string(allBytes)
	masked := 0
	if redact.SensitivePath(a.path) || redact.SensitivePath(a.display) {
		text, masked = redact.MaskValues(text)
	}
	lines := strings.Split(text, "\n")
	if len(allBytes) > 0 && allBytes[len(allBytes)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	out, truncated := formatRead(a.display, lines, a.startLine, a.endLine)
	if truncated {
		out += "\n... [truncated; set start_line to read the rest]"
	}
	meta := formatSize(int64(len(allBytes)))
	if masked > 0 {
		meta = fmt.Sprintf("masked %d values", masked)
	}
	return Result{Output: out, Meta: meta}, nil
}

type listDirTool struct{ guard *safety.Guard }

func newListDir(g *safety.Guard) *listDirTool { return &listDirTool{guard: g} }

func (t *listDirTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "list_dir",
			Description: "List a directory. Directories are suffixed with /. At most 200 entries, sorted by name.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory path relative to the current directory. Defaults to the current directory."}}}`),
		},
	}
}

type listDirArgs struct {
	Path string `json:"path"`
}

type listDirAction struct {
	path    string
	display string
}

func (t *listDirTool) Prepare(rawArgs string) (Action, error) {
	raw := strings.TrimSpace(rawArgs)
	if raw == "" {
		raw = "{}"
	}
	var args listDirArgs
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode list_dir: %w", err)
	}
	display := args.Path
	if display == "" {
		display = "."
	}
	resolved, err := t.guard.ResolvePath(display)
	if err != nil {
		return nil, err
	}
	return &listDirAction{path: resolved, display: display}, nil
}

func (a *listDirAction) Summary() string  { return "list " + a.display }
func (a *listDirAction) Preview() string  { return "" }
func (a *listDirAction) Kind() ActionKind { return ReadOnly }

func (a *listDirAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	entries, err := os.ReadDir(a.path)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	truncated := false
	if len(names) > listDirCap {
		names = names[:listDirCap]
		truncated = true
	}
	out := strings.Join(names, "\n")
	if truncated {
		out += fmt.Sprintf("\n... [%d more entries]", len(entries)-listDirCap)
	}
	if out == "" {
		out = "(empty)"
	}
	return Result{Output: out, Meta: fmt.Sprintf("%d entries", len(entries))}, nil
}

func formatRead(display string, lines []string, start, end int) (string, bool) {
	n := len(lines)
	if start < 1 {
		start = 1
	}
	if end < 1 || end > n {
		end = n
	}
	if n == 0 {
		return fmt.Sprintf("%s (lines 0-0 of 0)\n", display), false
	}
	if start > n {
		return fmt.Sprintf("error: start_line %d past end (%d lines)", start, n), false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (lines %d-%d of %d)\n", display, start, end, n)
	trunc := false
	size := 0
	shown := 0
	last := start - 1
	for i := start; i <= end; i++ {
		line := fmt.Sprintf("%6d\t%s\n", i, lines[i-1])
		if shown >= readLineCap || size+len(line) > readFileCap {
			trunc = true
			break
		}
		b.WriteString(line)
		size += len(line)
		shown++
		last = i
	}
	if last < end {
		trunc = true
	}
	return b.String(), trunc
}

func formatSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

func firstLines(s string, n int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n(%d more lines)", len(lines)-n)
}
