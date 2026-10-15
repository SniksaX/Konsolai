package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

type Recorder interface {
	Message(ctx context.Context, m llm.Message) error
	Action(ctx context.Context, a ActionRecord) error
	FileChange(ctx context.Context, c tools.FileChange) error
}

type ActionRecord struct {
	Seq          int
	Tool         string
	Args         json.RawMessage
	Kind         tools.ActionKind
	Decision     string
	RejectReason string
	IsError      bool
	Meta         string
	Duration     time.Duration
	Output       string
}

type NopRecorder struct{}

func (NopRecorder) Message(context.Context, llm.Message) error         { return nil }
func (NopRecorder) Action(context.Context, ActionRecord) error         { return nil }
func (NopRecorder) FileChange(context.Context, tools.FileChange) error { return nil }

type Confirmer interface {
	Confirm(summary, preview string) (ui.Decision, string, error)
	ConfirmForced(summary, preview string) (ui.Decision, string, error)
}

type Config struct {
	Client              *llm.Client
	Tools               *tools.Registry
	Confirmer           Confirmer
	Out                 io.Writer
	MaxSteps            int
	Thinking            bool
	RequirePlan         bool
	ContextBudgetTokens int
	Prompt              string
	Temperature         float64
	Recorder            Recorder
	History             []llm.Message
	AutoApprove         bool
	ExploreBudget       int
	ExploreNote         string
}

var (
	ErrAborted  = errors.New("agent: aborted")
	ErrMaxSteps = errors.New("agent: max steps exceeded")
	ErrNoAnswer = errors.New("agent: no answer")
)

const elidedOutput = "[output elided to save context]"

func Run(ctx context.Context, cfg Config, systemContext, task string) error {
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = ActPrompt
	}
	temp := cfg.Temperature
	if temp == 0 {
		temp = 0.2
	}
	rec := cfg.Recorder
	if rec == nil {
		rec = NopRecorder{}
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt + "\n\n" + systemContext},
	}
	msgs = append(msgs, cfg.History...)
	userMsg := llm.Message{Role: llm.RoleUser, Content: task}
	msgs = append(msgs, userMsg)
	_ = rec.Message(ctx, userMsg)
	opts := llm.ChatOptions{Thinking: cfg.Thinking, Temperature: temp}
	specs := cfg.Tools.Specs()
	seenFail := make(map[string]struct{})
	seenRead := make(map[string]struct{})
	planApproved := false
	var planFiles []string
	explore := 0

	for step := 0; step < cfg.MaxSteps; step++ {
		elideToolOutputs(msgs, cfg.ContextBudgetTokens)
		clear := ui.Status(cfg.Out, "planning...")
		msg, err := cfg.Client.Complete(ctx, msgs, specs, opts)
		clear()
		if err != nil {
			return err
		}
		msgs = append(msgs, msg)
		_ = rec.Message(ctx, msg)

		if len(msg.ToolCalls) == 0 {
			text := trimLeadingBlanks(msg.Content)
			if strings.TrimSpace(text) == "" {
				return ErrNoAnswer
			}
			fmt.Fprintln(cfg.Out, text)
			return nil
		}
		if text := trimLeadingBlanks(msg.Content); strings.TrimSpace(text) != "" {
			fmt.Fprintln(cfg.Out, ui.Dim(text))
		}

		for _, call := range msg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			tool, ok := cfg.Tools.Get(call.Function.Name)
			args := json.RawMessage(call.Function.Arguments)
			if !ok {
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: unknown tool " + call.Function.Name,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				continue
			}
			action, err := tool.Prepare(call.Function.Arguments)
			if err != nil {
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + err.Error(),
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				continue
			}
			kind := action.Kind()
			if kind == tools.Mutating && cfg.RequirePlan && !planApproved {
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: plan not approved. Explore with read-only tools, then call propose_plan.",
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "gate_blocked", IsError: true, Output: tm.Content})
				continue
			}
			decision := "auto"
			summary := action.Summary()
			outside := false
			if kind == tools.Mutating && cfg.RequirePlan && planApproved {
				if p, ok := filePathOf(action); ok && !inPlan(planFiles, p) {
					outside = true
					summary = "⚠ outside plan: " + summary
				}
			}
			if kind != tools.ReadOnly && kind != tools.Final {
				var d ui.Decision
				var reason string
				var err error
				if outside && !cfg.AutoApprove {
					d, reason, err = cfg.Confirmer.ConfirmForced(summary, action.Preview())
				} else {
					if outside && cfg.AutoApprove {
						fmt.Fprintln(cfg.Out, ui.Yellow(summary))
					}
					d, reason, err = cfg.Confirmer.Confirm(summary, action.Preview())
				}
				if err != nil {
					return err
				}
				switch d {
				case ui.Reject:
					tm := llm.Message{
						Role:       llm.RoleTool,
						Content:    "user rejected: " + reason,
						ToolCallID: call.ID,
					}
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "rejected", RejectReason: reason, IsError: true, Output: tm.Content})
					continue
				case ui.Quit:
					return ErrAborted
				case ui.Approve:
					decision = "approved"
					if cfg.AutoApprove {
						decision = "auto"
					}
					if kind == tools.Plan {
						planApproved = true
						planFiles = planFilesOf(action)
					}
				case ui.ApproveAll:
					decision = "approved_all"
					if cfg.AutoApprove {
						decision = "auto"
					}
					if kind == tools.Plan {
						planApproved = true
						planFiles = planFilesOf(action)
					}
				}
			}
			finish := action.Kind() == tools.Final
			if kind == tools.ReadOnly {
				key := call.Function.Name + "\x00" + canonicalJSON(call.Function.Arguments)
				if _, dup := seenRead[key]; dup {
					if !finish {
						fmt.Fprintln(cfg.Out, ui.Cyan("▸ ")+summary+" (duplicate, skipped)")
					}
					tm := llm.Message{
						Role: llm.RoleTool, Content: "note: you already made this exact call; its result is above.",
						ToolCallID: call.ID,
					}
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					continue
				}
				seenRead[key] = struct{}{}
			}
			if !finish {
				fmt.Fprintln(cfg.Out, ui.Cyan("▸ ")+summary)
			}
			start := time.Now()
			result, err := action.Run(ctx)
			elapsed := time.Since(start)
			if err != nil {
				return err
			}
			_ = rec.Action(ctx, ActionRecord{
				Tool: call.Function.Name, Args: args, Kind: kind, Decision: decision,
				IsError: result.IsError, Meta: result.Meta, Duration: elapsed, Output: result.Output,
			})
			if result.Change != nil {
				_ = rec.FileChange(ctx, *result.Change)
			}
			if result.Final {
				text := trimLeadingBlanks(result.Output)
				if strings.TrimSpace(text) == "" {
					return ErrNoAnswer
				}
				fmt.Fprintln(cfg.Out)
				fmt.Fprintln(cfg.Out, ui.Bold(text))
				return nil
			}
			mark := ui.Green("✓")
			if result.IsError {
				mark = ui.Red("✗")
			}
			fmt.Fprintf(cfg.Out, "%s %s · %s\n", mark, displayMeta(result), formatDuration(elapsed))
			content := result.Output
			if kind == tools.ReadOnly {
				if !cfg.RequirePlan || !planApproved {
					explore++
				}
			} else if kind != tools.Plan {
				explore = 0
			}
			if cfg.ExploreBudget > 0 && explore >= cfg.ExploreBudget && cfg.ExploreNote != "" {
				content += "\n\n" + cfg.ExploreNote
			}
			if result.IsError {
				sum := sha256.Sum256([]byte(result.Output))
				key := call.Function.Name + call.Function.Arguments + string(sum[:])
				if _, dup := seenFail[key]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					seenFail[key] = struct{}{}
				}
			}
			tm := llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: call.ID,
			}
			msgs = append(msgs, tm)
			_ = rec.Message(ctx, tm)
		}
	}
	return ErrMaxSteps
}

func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func trimLeadingBlanks(s string) string {
	return strings.TrimLeft(s, "\r\n")
}

func displayMeta(r tools.Result) string {
	if r.Meta != "" {
		return r.Meta
	}
	s := strings.TrimSpace(r.Output)
	s = strings.TrimPrefix(s, "error: ")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func tokenEstimate(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, c := range m.ToolCalls {
			n += len(c.Function.Name) + len(c.Function.Arguments)
		}
	}
	return n / 4
}

func elideToolOutputs(msgs []llm.Message, budget int) {
	if budget <= 0 {
		return
	}
	for tokenEstimate(msgs) > budget {
		limit := len(msgs) - 6
		if limit <= 0 {
			return
		}
		idx := -1
		for i := 0; i < limit; i++ {
			if msgs[i].Role == llm.RoleTool && msgs[i].Content != elidedOutput {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		msgs[idx].Content = elidedOutput
	}
}

func canonicalJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

func filePathOf(a tools.Action) (string, bool) {
	type hasPath interface{ FilePath() string }
	p, ok := a.(hasPath)
	if !ok {
		return "", false
	}
	return p.FilePath(), true
}

func planFilesOf(a tools.Action) []string {
	type hasFiles interface{ PlanFiles() []string }
	p, ok := a.(hasFiles)
	if !ok {
		return nil
	}
	return p.PlanFiles()
}

func inPlan(files []string, path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	for _, f := range files {
		if strings.TrimPrefix(filepath.ToSlash(f), "./") == path {
			return true
		}
	}
	return false
}
