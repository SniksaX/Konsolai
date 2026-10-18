package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/rag"
)

type RecallFunc func(ctx context.Context, query, scope string, k int) ([]rag.Hit, error)

type recallTool struct {
	search RecallFunc
}

func newRecall(search RecallFunc) *recallTool {
	return &recallTool{search: search}
}

func (t *recallTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "recall",
			Description: "Search indexed sessions, code and fetched pages for earlier decisions or implementations.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"What to look for."},"scope":{"type":"string","enum":["sessions","code","web","all"],"description":"Restrict the search. Default all."},"k":{"type":"integer","description":"How many results to return (1-10, default 6)."}},"required":["query"]}`),
		},
	}
}

type recallArgs struct {
	Query string `json:"query"`
	Scope string `json:"scope"`
	K     int    `json:"k"`
}

type recallAction struct {
	search RecallFunc
	query  string
	scope  string
	k      int
}

func (t *recallTool) Prepare(rawArgs string) (Action, error) {
	var args recallArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode recall: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return nil, errors.New("tools: recall: query is required")
	}
	scope := strings.ToLower(strings.TrimSpace(args.Scope))
	if scope == "" {
		scope = "all"
	}
	switch scope {
	case "sessions", "code", "web", "all":
	default:
		return nil, fmt.Errorf("tools: recall: scope must be sessions, code, web or all")
	}
	k := args.K
	if k == 0 {
		k = 6
	}
	if k < 1 {
		k = 1
	}
	if k > 10 {
		k = 10
	}
	return &recallAction{search: t.search, query: args.Query, scope: scope, k: k}, nil
}

func (a *recallAction) Summary() string  { return "recall " + a.query }
func (a *recallAction) Preview() string  { return "" }
func (a *recallAction) Kind() ActionKind { return ReadOnly }

func (a *recallAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if a.search == nil {
		return Result{Output: "recall is not available", IsError: true}, nil
	}
	hits, err := a.search(ctx, a.query, a.scope, a.k)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	out := rag.FormatHits(hits)
	return Result{Output: out, Meta: fmt.Sprintf("%d results", len(hits))}, nil
}
