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
	Task                string
	RouterTime          time.Duration
	Err                 io.Writer
	Stats               *Stats
	Plain               bool
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
	fmt.Fprintln(w, ui.Current().Muted(FormatSummary(intent, s)))
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
	var planCmds []string
	explore := 0
	noteSent := false
	readonlyAfterNote := 0
	sourceReads := 0
	toolsGone := false
	goneNoteAdded := false
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

	for step := 0; step < analysisLimit(cfg, sourceReads); step++ {
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
		if toolsGone && !goneNoteAdded {
			gone := llm.Message{Role: llm.RoleUser, Content: ToolsGoneNote}
			msgs = append(msgs, gone)
			_ = rec.Message(ctx, gone)
			goneNoteAdded = true
		}
		stepSpecs := specs
		if toolsGone {
			if cfg.RequirePlan && !planApproved {
				stepSpecs = planOnlySpecs(specs)
			} else if !cfg.RequirePlan {
				stepSpecs = nil
			}
		}
		clear := ui.Start(label)
		t0 := time.Now()
		msg, usage, err := cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
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
			fmt.Fprintln(cfg.Out)
			writeAnswer(cfg.Out, text, cfg.Plain)
			return nil
		}
		if text := trimLeadingBlanks(msg.Content); strings.TrimSpace(text) != "" {
			fmt.Fprintln(cfg.Out, ui.Current().Muted(text))
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
			if noteSent && kind == tools.ReadOnly {
				readonlyAfterNote++
				if readonlyAfterNote >= hardStopAfter(cfg, sourceReads) {
					toolsGone = true
				}
			}
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
			inPlanHit := false
			if kind == tools.Mutating && cfg.RequirePlan && planApproved {
				if p, ok := filePathOf(action); ok {
					if inPlan(planFiles, p) {
						inPlanHit = true
					} else {
						outside = true
					}
				} else if c, ok := commandOf(action); ok {
					if inPlanCmd(planCmds, c) {
						inPlanHit = true
					} else {
						outside = true
					}
				} else {
					outside = true
				}
				if outside {
					summary = "⚠ outside plan: " + summary
				}
			}
			if inPlanHit {
				summary += " " + ui.Current().Muted("(in plan)")
			}
			if !inPlanHit && kind != tools.ReadOnly && kind != tools.Final {
				var d ui.Decision
				var reason string
				var err error
				if outside && !cfg.AutoApprove {
					d, reason, err = cfg.Confirmer.ConfirmForced(summary, action.Preview())
				} else {
					if outside && cfg.AutoApprove {
						fmt.Fprintln(cfg.Out, ui.Current().Warn(summary))
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
						planCmds = planCmdsOf(action)
					}
				case ui.ApproveAll:
					decision = "approved_all"
					if cfg.AutoApprove {
						decision = "auto"
					}
					if kind == tools.Plan {
						planApproved = true
						planFiles = planFilesOf(action)
						planCmds = planCmdsOf(action)
					}
				}
			}
			finish := action.Kind() == tools.Final
			if kind == tools.ReadOnly {
				key := call.Function.Name + "\x00" + canonicalJSON(call.Function.Arguments)
				if _, dup := seenRead[key]; dup {
					if !finish {
						fmt.Fprintln(cfg.Out, ui.Current().Accent("▸")+" "+summary+" (duplicate, skipped)")
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
				fmt.Fprintln(cfg.Out, ui.Current().Accent("▸")+" "+summary)
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
				writeAnswer(cfg.Out, text, cfg.Plain)
				return nil
			}
			pal := ui.Current()
			mark := pal.Success("✓")
			if result.IsError {
				mark = pal.Error("✗")
			}
			fmt.Fprintf(cfg.Out, "%s %s · %s\n", mark, pal.Muted(displayMeta(result)), pal.Muted(formatDuration(elapsed)))
			content := result.Output
			if kind == tools.ReadOnly {
				if !cfg.RequirePlan || !planApproved {
					explore++
				}
			} else if kind != tools.Plan {
				explore = 0
			}
			if cfg.Task == "analysis" && analysisSourceRead(call.Function.Name, call.Function.Arguments) && !result.IsError {
				sourceReads++
			}
			if cfg.ExploreBudget > 0 && explore >= cfg.ExploreBudget && cfg.ExploreNote != "" {
				content += "\n\n" + exploreNote(cfg, sourceReads)
				noteSent = true
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
	note := llm.Message{Role: llm.RoleUser, Content: MaxStepsNote}
	msgs = append(msgs, note)
	_ = rec.Message(ctx, note)
	clear := ui.Start("thinking")
	t0 := time.Now()
	msg, usage, err := cfg.Client.Complete(ctx, msgs, nil, llm.ChatOptions{Thinking: false, Temperature: temp})
	clear()
	if err == nil {
		st.Steps++
		st.ModelTime += time.Since(t0)
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		text := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(text) != "" {
			fmt.Fprintln(cfg.Out)
			writeAnswer(cfg.Out, text, cfg.Plain)
		}
	}
	return ErrMaxSteps
}

func planOnlySpecs(specs []llm.ToolSpec) []llm.ToolSpec {
	for _, s := range specs {
		if s.Function.Name == "propose_plan" {
			return []llm.ToolSpec{s}
		}
	}
	return nil
}

func writeAnswer(w io.Writer, text string, plain bool) {
	if plain {
		fmt.Fprintln(w, text)
		return
	}
	r := ui.NewRenderer(w, false)
	_, _ = r.Write([]byte(text))
	if !strings.HasSuffix(text, "\n") {
		_, _ = r.Write([]byte("\n"))
	}
	_ = r.Flush()
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

func planCmdsOf(a tools.Action) []string {
	type hasCmds interface{ PlanCommands() []string }
	p, ok := a.(hasCmds)
	if !ok {
		return nil
	}
	return p.PlanCommands()
}

func commandOf(a tools.Action) (string, bool) {
	type hasCmd interface{ PlanCommand() string }
	p, ok := a.(hasCmd)
	if !ok {
		return "", false
	}
	return p.PlanCommand(), true
}

func normCmd(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func inPlanCmd(cmds []string, cmd string) bool {
	if matchDeclared(cmds, cmd) {
		return true
	}
	segs := splitPlanSegments(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, s := range segs {
		if !matchDeclared(cmds, s) {
			return false
		}
	}
	return true
}

func matchDeclared(cmds []string, cmd string) bool {
	want := normCmd(cmd)
	for _, c := range cmds {
		if normCmd(c) == want {
			return true
		}
	}
	return false
}

func splitPlanSegments(cmd string) []string {
	var segs []string
	var b strings.Builder
	var quote byte
	flush := func() {
		s := strings.TrimSpace(b.String())
		b.Reset()
		if s != "" {
			segs = append(segs, s)
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if c == '\\' && i+1 < len(cmd) {
			b.WriteByte(c)
			b.WriteByte(cmd[i+1])
			i++
			continue
		}
		if c == '\n' || c == '\r' || c == ';' {
			flush()
			continue
		}
		if c == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
			flush()
			i++
			continue
		}
		b.WriteByte(c)
	}
	flush()
	return segs
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

func analysisLimit(cfg Config, sourceReads int) int {
	n := cfg.MaxSteps
	if n < 1 {
		n = 15
	}
	if cfg.Task == "analysis" && sourceReads < 3 {
		return n + 6
	}
	return n
}

func hardStopAfter(cfg Config, sourceReads int) int {
	if cfg.Task == "analysis" && sourceReads < 3 {
		return 8
	}
	return 2
}

func exploreNote(cfg Config, sourceReads int) string {
	if cfg.Task == "analysis" && sourceReads < 3 {
		return AnalysisExploreNote
	}
	return cfg.ExploreNote
}

func analysisSourceRead(name, args string) bool {
	if name != "read_file" && name != "outline" {
		return false
	}
	var p struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &p) != nil {
		return false
	}
	base := strings.ToLower(filepath.Base(p.Path))
	switch base {
	case "package.json", "go.mod", "go.sum", "pyproject.toml", "requirements.txt", "cargo.toml", "cargo.lock":
		return false
	}
	if strings.HasPrefix(base, "readme") {
		return false
	}
	return p.Path != ""
}
