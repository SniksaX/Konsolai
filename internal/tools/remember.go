package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
)

type rememberTool struct{ facts FactStore }

func newRemember(facts FactStore) *rememberTool { return &rememberTool{facts: facts} }

func (t *rememberTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "remember",
			Description: "Store a fact the user explicitly asked you to remember. Never call this on your own initiative.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"fact":{"type":"string","description":"The fact to remember."},"scope":{"type":"string","description":"user or project. Default user.","enum":["user","project"]}},"required":["fact"]}`),
		},
	}
}

type rememberArgs struct {
	Fact  string `json:"fact"`
	Scope string `json:"scope"`
}

type rememberAction struct {
	facts FactStore
	fact  string
	scope string
}

func (t *rememberTool) Prepare(rawArgs string) (Action, error) {
	var args rememberArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode remember: %w", err)
	}
	if strings.TrimSpace(args.Fact) == "" {
		return nil, errors.New("tools: remember: fact is required")
	}
	scope := args.Scope
	if scope == "" {
		scope = "user"
	}
	if scope != "user" && scope != "project" {
		return nil, fmt.Errorf("tools: remember: scope must be user or project")
	}
	return &rememberAction{facts: t.facts, fact: args.Fact, scope: scope}, nil
}

func (a *rememberAction) Summary() string  { return "remember: " + a.fact }
func (a *rememberAction) Preview() string  { return a.scope + ": " + a.fact }
func (a *rememberAction) Kind() ActionKind { return Mutating }

func (a *rememberAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := a.facts.Add(ctx, a.fact, a.scope); err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	return Result{Output: "remembered", Meta: a.scope}, nil
}
