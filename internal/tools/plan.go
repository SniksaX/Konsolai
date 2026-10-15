package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
)

type proposePlanTool struct{}

func newProposePlan() *proposePlanTool { return &proposePlanTool{} }

func (t *proposePlanTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "propose_plan",
			Description: "Propose an implementation plan. Call this after exploring with read-only tools and before any mutating edits. Steps should name the files you will change.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"steps":{"type":"array","items":{"type":"string"}},"files":{"type":"array","items":{"type":"string"},"description":"Repo-relative paths this plan will create or modify."}},"required":["summary","steps","files"]}`),
		},
	}
}

type proposePlanArgs struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`
	Files   []string `json:"files"`
}

type proposePlanAction struct {
	summary string
	steps   []string
	files   []string
}

func (t *proposePlanTool) Prepare(rawArgs string) (Action, error) {
	var args proposePlanArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode propose_plan: %w", err)
	}
	if strings.TrimSpace(args.Summary) == "" {
		return nil, errors.New("tools: propose_plan: summary is required")
	}
	if len(args.Steps) < 1 {
		return nil, errors.New("tools: propose_plan: at least one step is required")
	}
	if len(args.Files) < 1 {
		return nil, errors.New("tools: propose_plan: files is required")
	}
	return &proposePlanAction{summary: args.Summary, steps: args.Steps, files: args.Files}, nil
}

func (a *proposePlanAction) PlanFiles() []string { return a.files }

func (a *proposePlanAction) Summary() string { return "plan: " + a.summary }

func (a *proposePlanAction) Preview() string {
	var b strings.Builder
	if len(a.files) > 0 {
		b.WriteString("files: ")
		b.WriteString(strings.Join(a.files, ", "))
		b.WriteByte('\n')
	}
	for i, s := range a.steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (a *proposePlanAction) Kind() ActionKind { return Plan }

func (a *proposePlanAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Output: "plan approved", Meta: "plan"}, nil
}
