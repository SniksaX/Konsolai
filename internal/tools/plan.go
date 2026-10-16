package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

const PlanExportReject = "plan must produce the PDF/HTML with the export_doc tool (write Markdown first, then export_doc)"
const PlanCommandReject = "list the exact command lines you will run; for a multi-line script, put the whole script as one command entry"

type proposePlanTool struct {
	docsTask bool
	request  string
}

func newProposePlan(docsTask bool, request string) *proposePlanTool {
	return &proposePlanTool{docsTask: docsTask, request: request}
}

func mentionsDocExport(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "pdf") || strings.Contains(l, "html")
}

func mentionsExportDoc(summary string, steps []string) bool {
	blob := strings.ToLower(summary + " " + strings.Join(steps, " "))
	return strings.Contains(blob, "export_doc")
}

func (t *proposePlanTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "propose_plan",
			Description: "Propose an implementation plan. Call this after exploring with read-only tools and before any mutating edits. Steps should name the files you will change.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"steps":{"type":"array","items":{"type":"string"}},"files":{"type":"array","items":{"type":"string"},"description":"Repo-relative paths this plan will create or modify."},"commands":{"type":"array","items":{"type":"string"},"description":"Exact shell commands this plan will run."}},"required":["summary","steps","files"]}`),
		},
	}
}

type proposePlanArgs struct {
	Summary  string   `json:"summary"`
	Steps    []string `json:"steps"`
	Files    []string `json:"files"`
	Commands []string `json:"commands"`
}

type proposePlanAction struct {
	summary  string
	steps    []string
	files    []string
	commands []string
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
	if t.docsTask && mentionsDocExport(t.request) && !mentionsExportDoc(args.Summary, args.Steps) {
		return nil, errors.New(PlanExportReject)
	}
	for _, c := range args.Commands {
		if placeholderCmd(c) {
			return nil, errors.New(PlanCommandReject)
		}
		if err := safety.PlanCommandForbidden(c); err != nil {
			return nil, err
		}
	}
	return &proposePlanAction{summary: args.Summary, steps: args.Steps, files: args.Files, commands: args.Commands}, nil
}

func placeholderCmd(s string) bool {
	if strings.Contains(s, "(all") || strings.Contains(s, "…") || strings.Contains(s, "...") || strings.Contains(s, "<...>") {
		return true
	}
	low := strings.ToLower(s)
	for _, w := range strings.Fields(low) {
		w = strings.Trim(w, ".,:;()[]")
		if w == "listed" || w == "each" || w == "etc" {
			return true
		}
	}
	return false
}

func (a *proposePlanAction) PlanFiles() []string    { return a.files }
func (a *proposePlanAction) PlanCommands() []string { return a.commands }
func (a *proposePlanAction) PlanText() string {
	var b strings.Builder
	b.WriteString(a.summary)
	b.WriteByte('\n')
	b.WriteString(strings.Join(a.steps, "\n"))
	b.WriteByte('\n')
	b.WriteString(strings.Join(a.files, "\n"))
	b.WriteByte('\n')
	b.WriteString(strings.Join(a.commands, "\n"))
	return b.String()
}

func (a *proposePlanAction) Summary() string { return "plan: " + a.summary }

func (a *proposePlanAction) Preview() string {
	var b strings.Builder
	for i, s := range a.steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	if len(a.files) > 0 {
		b.WriteString("files: ")
		b.WriteString(strings.Join(a.files, ", "))
		b.WriteByte('\n')
	}
	if len(a.commands) > 0 {
		b.WriteString("commands: ")
		b.WriteString(strings.Join(a.commands, ", "))
		b.WriteByte('\n')
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
