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

type ThinkingPolicy int

const (
	ThinkOff ThinkingPolicy = iota
	ThinkOn
	ThinkUntilPlan
)

type Config struct {
	Client              *llm.Client
	Tools               *tools.Registry
	Confirmer           Confirmer
	Out                 io.Writer
	MaxSteps            int
	ThinkingPolicy      ThinkingPolicy
	RequirePlan         bool
	ContextBudgetTokens int
	Prompt              string
	Temperature         float64
	Recorder            Recorder
	History             []llm.Message
	AutoApprove         bool
	ExploreBudget       int
	ExploreNote         string
	Trace               bool
	Intent              string
	RouterTime          time.Duration
	Err                 io.Writer
	Stats               *Stats
}

type Stats struct {
	Steps            int
	ModelTime        time.Duration
	ToolTime         time.Duration
	PromptTokens     int
	CompletionTokens int
	RouterTime       time.Duration
}

func FormatTokens(n int) string {
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func FormatSummary(intent string, s Stats) string {
	return fmt.Sprintf("· %s · %d steps · model %s · tools %s · %s in / %s out",
		intent, s.Steps, formatDuration(s.ModelTime), formatDuration(s.ToolTime),
		FormatTokens(s.PromptTokens), FormatTokens(s.CompletionTokens))
}

func FormatTrace(step int, think bool, d time.Duration, in, out int, names []string) string {
	th := "off"
	if think {
		th = "on"
	}
	return fmt.Sprintf("  [step %d] think=%s  %s  in=%s out=%s  -> %s",
		step, th, formatDuration(d), FormatTokens(in), FormatTokens(out), strings.Join(names, ", "))
}

func PrintSummary(w io.Writer, intent string, s Stats) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, ui.Dim(FormatSummary(intent, s)))
}

func PrintTrace(w io.Writer, step int, think bool, d time.Duration, in, out int, names []string) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, FormatTrace(step, think, d, in, out, names))
}

func (p ThinkingPolicy) enabled(planApproved, shellFail bool) bool {
	switch p {
	case ThinkOn:
		return true
	case ThinkUntilPlan:
		if !planApproved {
			return true
		}
		return shellFail
	default:
		return false
	}
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
	specs := cfg.Tools.Specs()
	seenFail := make(map[string]struct{})
	seenRead := make(map[string]struct{})
	planApproved := false
	var planFiles []string
	explore := 0
	shellFail := false
	var st Stats
	st.RouterTime = cfg.RouterTime
	defer func() {
		if cfg.Stats != nil {
			*cfg.Stats = st
		}
		if cfg.Intent != "" || cfg.Err != nil {
			intent := cfg.Intent
			if intent == "" {
				intent = "act"
			}
			PrintSummary(cfg.Err, intent, st)
		}
	}()

	for step := 0; step < cfg.MaxSteps; step++ {
		elideToolOutputs(msgs, cfg.ContextBudgetTokens)
		think := cfg.ThinkingPolicy.enabled(planApproved, shellFail)
		opts := llm.ChatOptions{Thinking: think, Temperature: temp}
		shellFail = false
		label := "working"
		if cfg.RequirePlan && !planApproved {
			label = "planning"
		} else if think {
			label = "thinking"
		}
		clear := ui.Start(label)
		t0 := time.Now()
		msg, usage, err := cfg.Client.Complete(ctx, msgs, specs, opts)
		modelElapsed := time.Since(t0)
		clear()
		if err != nil {
			return err
		}
		st.Steps++
		st.ModelTime += modelElapsed
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		var names []string
		for _, c := range msg.ToolCalls {
			names = append(names, c.Function.Name)
		}
		if cfg.Trace {
			PrintTrace(cfg.Err, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, names)
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
			st.ToolTime += elapsed
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
			if call.Function.Name == "run_shell" && result.IsError {
				shellFail = true
			}
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
