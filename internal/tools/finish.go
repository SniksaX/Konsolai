package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
)

type finishTool struct{}

func newFinish() *finishTool { return &finishTool{} }

func (t *finishTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "finish",
			Description: "End the task. Call exactly once, after all work is done, with a summary of at most 5 lines. Do not start servers; put the command to run in the summary.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","description":"Final summary for the user, at most 5 lines."}},"required":["summary"]}`),
		},
	}
}

type finishArgs struct {
	Summary string `json:"summary"`
}

type finishAction struct {
	summary string
}

func (t *finishTool) Prepare(rawArgs string) (Action, error) {
	var args finishArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode finish: %w", err)
	}
	if strings.TrimSpace(args.Summary) == "" {
		return nil, errors.New("tools: finish: summary is required")
	}
	return &finishAction{summary: args.Summary}, nil
}

func (a *finishAction) Summary() string  { return "finish" }
func (a *finishAction) Preview() string  { return a.summary }
func (a *finishAction) Kind() ActionKind { return Final }

func (a *finishAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Output: a.summary, Final: true}, nil
}
