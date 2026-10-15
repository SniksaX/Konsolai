package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/siraj/konsolai/internal/diff"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

type editFileTool struct{ guard *safety.Guard }

func newEditFile(g *safety.Guard) *editFileTool { return &editFileTool{guard: g} }

func (t *editFileTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "edit_file",
			Description: "Replace old_str with new_str in an existing file. old_str must be unique unless replace_all is true. Include enough surrounding context.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_str","new_str"]}`),
		},
	}
}

type editFileArgs struct {
	Path       string `json:"path"`
	OldStr     string `json:"old_str"`
	NewStr     string `json:"new_str"`
	ReplaceAll bool   `json:"replace_all"`
}

type editFileAction struct {
	path    string
	display string
	oldStr  string
	newStr  string
	all     bool
	oldText string
	newText string
}

func (t *editFileTool) Prepare(rawArgs string) (Action, error) {
	var args editFileArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode edit_file: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("tools: edit_file: path is required")
	}
	if args.OldStr == "" {
		return nil, errors.New("tools: edit_file: old_str must be non-empty")
	}
	if args.OldStr == args.NewStr {
		return nil, errors.New("tools: edit_file: old_str and new_str must differ")
	}
	resolved, err := t.guard.ResolvePath(args.Path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("tools: edit_file: %s does not exist", args.Path)
		}
		return nil, fmt.Errorf("tools: edit_file: %w", err)
	}
	oldText := string(b)
	n := strings.Count(oldText, args.OldStr)
	if n == 0 {
		return nil, errors.New("old_str not found")
	}
	if n > 1 && !args.ReplaceAll {
		return nil, fmt.Errorf("old_str matches %d times; add surrounding context or set replace_all", n)
	}
	newText := strings.Replace(oldText, args.OldStr, args.NewStr, -1)
	if !args.ReplaceAll {
		newText = strings.Replace(oldText, args.OldStr, args.NewStr, 1)
	}
	return &editFileAction{
		path: resolved, display: args.Path, oldStr: args.OldStr, newStr: args.NewStr,
		all: args.ReplaceAll, oldText: oldText, newText: newText,
	}, nil
}

func (a *editFileAction) Summary() string  { return "edit " + a.display }
func (a *editFileAction) Preview() string  { return diff.Unified(a.display, a.oldText, a.newText, 3) }
func (a *editFileAction) Kind() ActionKind { return Mutating }

func (a *editFileAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := atomicWrite(a.path, a.newText); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	add, rem := diff.Stat(a.oldText, a.newText)
	return Result{
		Output: fmt.Sprintf("edited %s (+%d -%d)", a.display, add, rem),
		Meta:   fmt.Sprintf("+%d -%d", add, rem),
	}, nil
}
