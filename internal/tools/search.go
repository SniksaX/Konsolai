package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

const (
	searchMaxFile = 1 << 20
	searchPeek    = 8 << 10
	searchCap     = 200
	searchTextCap = 200
)

type searchTool struct {
	guard *safety.Guard
	files FileLister
}

func newSearch(g *safety.Guard, files FileLister) *searchTool {
	return &searchTool{guard: g, files: files}
}

func (t *searchTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "search",
			Description: "Search file contents with a RE2 regexp. Skips files over 1 MiB and binary files. Optional path prefix and glob limit the set.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"RE2 regular expression."},"path":{"type":"string","description":"Optional directory prefix relative to the root."},"glob":{"type":"string","description":"Optional glob; without a slash matches the base name."},"case_insensitive":{"type":"boolean"}},"required":["pattern"]}`),
		},
	}
}

type searchArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	Glob            string `json:"glob"`
	CaseInsensitive bool   `json:"case_insensitive"`
}

type searchAction struct {
	guard   *safety.Guard
	files   FileLister
	re      *regexp.Regexp
	prefix  string
	glob    string
	globRe  *regexp.Regexp
	pattern string
}

func (t *searchTool) Prepare(rawArgs string) (Action, error) {
	var args searchArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode search: %w", err)
	}
	if args.Pattern == "" {
		return nil, errors.New("tools: search: pattern is required")
	}
	pat := args.Pattern
	if args.CaseInsensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("tools: search: %w", err)
	}
	var globRe *regexp.Regexp
	if args.Glob != "" {
		globRe, err = compileGlob(args.Glob)
		if err != nil {
			return nil, fmt.Errorf("tools: search glob: %w", err)
		}
	}
	return &searchAction{guard: t.guard, files: t.files, re: re, prefix: args.Path, glob: args.Glob, globRe: globRe, pattern: args.Pattern}, nil
}

func (a *searchAction) Summary() string  { return "search " + a.pattern }
func (a *searchAction) Preview() string  { return "" }
func (a *searchAction) Kind() ActionKind { return ReadOnly }

func (a *searchAction) Run(ctx context.Context) (Result, error) {
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
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if !hasPathPrefix(rel, a.prefix) {
			continue
		}
		if a.globRe != nil && !matchGlob(a.globRe, a.glob, rel) {
			continue
		}
		resolved, err := a.guard.ResolvePath(rel)
		if err != nil {
			continue
		}
		n, skip, err := searchFile(resolved, rel, a.re, searchCap-len(hits))
		if err != nil {
			continue
		}
		if skip {
			continue
		}
		total += n.count
		hits = append(hits, n.lines...)
		if len(hits) >= searchCap {
			break
		}
	}
	more := total - len(hits)
	out := strings.Join(hits, "\n")
	if more > 0 {
		out += fmt.Sprintf("\n... %d more matches", more)
	}
	if out == "" {
		out = "(no matches)"
	}
	return Result{Output: out, Meta: fmt.Sprintf("%d matches", total)}, nil
}

type searchHits struct {
	lines []string
	count int
}

func searchFile(abs, rel string, re *regexp.Regexp, room int) (searchHits, bool, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return searchHits{}, true, err
	}
	if fi.IsDir() || fi.Size() > searchMaxFile {
		return searchHits{}, true, nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return searchHits{}, true, err
	}
	defer f.Close()
	peek := make([]byte, searchPeek)
	n, _ := io.ReadFull(f, peek)
	if bytes.IndexByte(peek[:n], 0) >= 0 {
		return searchHits{}, true, nil
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		return searchHits{}, true, err
	}
	data := append(peek[:n], rest...)
	var hits searchHits
	for i, line := range strings.Split(string(data), "\n") {
		if !re.MatchString(line) {
			continue
		}
		hits.count++
		if len(hits.lines) >= room {
			continue
		}
		text := line
		if len(text) > searchTextCap {
			text = text[:searchTextCap]
		}
		hits.lines = append(hits.lines, fmt.Sprintf("%s:%d: %s", rel, i+1, text))
	}
	return hits, false, nil
}
