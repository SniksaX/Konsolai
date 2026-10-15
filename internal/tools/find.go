package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

const findCap = 500

type findFilesTool struct {
	guard *safety.Guard
	files FileLister
}

func newFindFiles(g *safety.Guard, files FileLister) *findFilesTool {
	return &findFilesTool{guard: g, files: files}
}

func (t *findFilesTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "find_files",
			Description: "Find files by glob. ** matches any number of directories, * does not cross /, ? is one character. A glob without a slash matches the base name.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"glob":{"type":"string","description":"File glob."},"path":{"type":"string","description":"Optional directory prefix relative to the root."}},"required":["glob"]}`),
		},
	}
}

type findFilesArgs struct {
	Glob string `json:"glob"`
	Path string `json:"path"`
}

type findFilesAction struct {
	files  FileLister
	glob   string
	prefix string
	re     *regexp.Regexp
}

func (t *findFilesTool) Prepare(rawArgs string) (Action, error) {
	var args findFilesArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode find_files: %w", err)
	}
	if args.Glob == "" {
		return nil, errors.New("tools: find_files: glob is required")
	}
	re, err := compileGlob(args.Glob)
	if err != nil {
		return nil, fmt.Errorf("tools: find_files glob: %w", err)
	}
	prefix, err := relPrefix(t.guard, args.Path)
	if err != nil {
		return nil, err
	}
	return &findFilesAction{files: t.files, glob: args.Glob, prefix: prefix, re: re}, nil
}

func (a *findFilesAction) Summary() string  { return "find " + a.glob }
func (a *findFilesAction) Preview() string  { return "" }
func (a *findFilesAction) Kind() ActionKind { return ReadOnly }

func (a *findFilesAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	files, err := a.files(ctx)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	var hits []string
	total := 0
	for _, rel := range files {
		if !hasPathPrefix(rel, a.prefix) {
			continue
		}
		if !matchGlob(a.re, a.glob, rel) {
			continue
		}
		total++
		if len(hits) < findCap {
			hits = append(hits, rel)
		}
	}
	out := strings.Join(hits, "\n")
	if total > len(hits) {
		out += fmt.Sprintf("\n... %d more files", total-len(hits))
	}
	if out == "" {
		out = "(no files)"
	}
	return Result{Output: out, Meta: fmt.Sprintf("%d files", total)}, nil
}
