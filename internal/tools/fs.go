package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

const (
	readFileCap  = 32 * 1024
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
			Description: "Create or overwrite a UTF-8 text file at path relative to the project root. Parent directories are created. Never use shell heredocs or echo redirection for file contents.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the project root."},"content":{"type":"string","description":"Full file contents to write."}},"required":["path","content"]}`),
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
	return &writeFileAction{path: resolved, display: args.Path, content: args.Content, overwrite: overwrite}, nil
}

func (a *writeFileAction) Summary() string {
	kind := "new"
	if a.overwrite {
		kind = "overwrite"
	}
	return fmt.Sprintf("write %s (%d B, %s)", a.display, len(a.content), kind)
}

func (a *writeFileAction) Preview() string {
	return firstLines(a.content, previewLines)
}

func (a *writeFileAction) ReadOnly() bool { return false }

func (a *writeFileAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0755); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	mode := os.FileMode(0644)
	if fi, err := os.Stat(a.path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".konsolai-write-*")
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := io.WriteString(f, a.content); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if err := f.Chmod(mode); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if err := f.Close(); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if err := os.Rename(tmp, a.path); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	cleanup = false
	return Result{Output: fmt.Sprintf("wrote %s (%d bytes)", a.display, len(a.content))}, nil
}

type readFileTool struct{ guard *safety.Guard }

func newReadFile(g *safety.Guard) *readFileTool { return &readFileTool{guard: g} }

func (t *readFileTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "read_file",
			Description: "Read a UTF-8 text file. Directories and binary files are rejected. Output is capped at 32 KiB.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the project root."}},"required":["path"]}`),
		},
	}
}

type readFileArgs struct {
	Path string `json:"path"`
}

type readFileAction struct {
	path    string
	display string
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
	return &readFileAction{path: resolved, display: args.Path}, nil
}

func (a *readFileAction) Summary() string { return "read " + a.display }
func (a *readFileAction) Preview() string { return "" }
func (a *readFileAction) ReadOnly() bool  { return true }

func (a *readFileAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	fi, err := os.Stat(a.path)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if fi.IsDir() {
		return Result{Output: "error: is a directory", IsError: true}, nil
	}
	f, err := os.Open(a.path)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	defer f.Close()
	buf := make([]byte, readFileCap+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	data := buf[:min(n, readFileCap)]
	if !utf8.Valid(data) {
		return Result{Output: "error: binary content (not UTF-8)", IsError: true}, nil
	}
	out := string(data)
	if n > readFileCap {
		out += fmt.Sprintf("\n... [truncated at %d bytes]", readFileCap)
	}
	return Result{Output: out}, nil
}

type listDirTool struct{ guard *safety.Guard }

func newListDir(g *safety.Guard) *listDirTool { return &listDirTool{guard: g} }

func (t *listDirTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "list_dir",
			Description: "List a directory. Directories are suffixed with /. At most 200 entries, sorted by name.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory path relative to the project root. Defaults to the root."}}}`),
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

func (a *listDirAction) Summary() string { return "list " + a.display }
func (a *listDirAction) Preview() string { return "" }
func (a *listDirAction) ReadOnly() bool  { return true }

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
	return Result{Output: out}, nil
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
