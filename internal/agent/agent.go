package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
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
	Client            *llm.Client
	Tools             *tools.Registry
	Confirmer         Confirmer
	Out               io.Writer
	MaxSteps          int
	ThinkingPolicy    ThinkingPolicy
	RequirePlan       bool
	Prompt            string
	Temperature       float64
	Recorder          Recorder
	History           []llm.Message
	AutoApprove       bool
	Approve           string
	TopP              float64
	ExploreBudget     int
	ExploreNote       string
	Trace             bool
	SessionID         string
	Intent            string
	Task              string
	RouterTime        time.Duration
	Err               io.Writer
	Stats             *Stats
	Plain             bool
	HideStats         bool
	OnShell           func(output string)
	Guard             *safety.Guard
	ResumePlan        bool
	PlanFiles         []string
	PlanCmds          []string
	PlanText          string
	TokenCap          int
	ThinkingMaxTokens int
	ElideTrigger      float64
	ElideTarget       float64
	IdenticalRejects  int
}

type Stats struct {
	Steps            int
	ModelTime        time.Duration
	ToolTime         time.Duration
	PromptTokens     int
	LastPromptTokens int
	CompletionTokens int
	ReasoningTokens  int
	RouterTime       time.Duration
}

var (
	ErrAborted         = errors.New("agent: aborted")
	ErrMaxSteps        = errors.New("agent: max steps exceeded")
	ErrNoAnswer        = errors.New("agent: no answer")
	ErrContextOverflow = errors.New("konsolai: the conversation no longer fits the model's context; start a new session with `new`.")
)

const (
	elidedPrefix      = "[tool output elided:"
	elidedReadPrefix  = "[elided] "
	contextSafety     = 1024
	contextTrigger    = 0.60
	contextTarget     = 0.40
	minOutputTokens   = 2048
	protectLastMsgs   = 6
	elideSummaryMax   = 80
	elideOutlineCap   = 1536
	postPlanNoteAt    = 4
	postPlanForceAt   = 6
	postPlanDocsAt    = 3
	docsExportNoteAt  = 2
	docsExportForceAt = 3
)

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
	if _, err := cfg.Client.ResolveModel(ctx); err != nil {
		return err
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt + "\n\n" + systemContext},
	}
	msgs = append(msgs, cfg.History...)
	userMsg := llm.Message{Role: llm.RoleUser, Content: task}
	msgs = append(msgs, userMsg)
	userIdx := len(msgs) - 1
	_ = rec.Message(ctx, userMsg)
	specs := cfg.Tools.Specs()
	seenFail := make(map[string]struct{})
	seenRead := make(map[string]struct{})
	prepRejectKey := ""
	prepRejects := 0
	var readSpans []readSpan
	planApproved := cfg.ResumePlan
	planFiles := cfg.PlanFiles
	planCmds := cfg.PlanCmds
	planText := cfg.PlanText
	if planApproved && cfg.Guard != nil {
		cfg.Guard.AllowPlanFiles(planFiles)
	}
	explore := 0
	noteSent := false
	forcePropose := false
	sawGateReject := false
	readonlyAfterNote := 0
	sourceReads := 0
	toolsGone := false
	goneNoteAdded := false
	wroteFile := false
	var verified []VerifiedCall
	exportedDoc := false
	docsNudged := false
	docsWriteNoted := false
	docsExportNoted := false
	docsExportWait := 0
	postPlanReads := 0
	forceMutating := false
	forceExport := false
	shellFail := false
	planRejected := false
	approvedAll := false
	printedBlock := false
	consecFail := 0
	skipSummary := false
	var st Stats
	st.RouterTime = cfg.RouterTime
	defer func() {
		if cfg.Stats != nil {
			*cfg.Stats = st
		}
		if skipSummary || cfg.HideStats {
			return
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
		think := cfg.ThinkingPolicy.enabled(planApproved, shellFail, consecFail)
		if cfg.ThinkingPolicy == ThinkPlanStep && planRejected && !planApproved {
			think = true
		}
		opts := llm.ChatOptions{Thinking: think, Temperature: temp, TopP: cfg.TopP}
		shellFail = false
		forcingPlan := toolsGone && cfg.RequirePlan && !planApproved
		if forcingPlan && cfg.ThinkingPolicy == ThinkPlanStep {
			think = true
			opts.Thinking = true
		}
		label := "working"
		if cfg.RequirePlan && !planApproved {
			label = "planning"
		} else if think {
			label = "thinking"
		}
		if toolsGone && !goneNoteAdded {
			note := ToolsGoneNote
			if forcingPlan {
				note = ForcedPlanNote
			}
			gone := llm.Message{Role: llm.RoleUser, Content: note}
			msgs = append(msgs, gone)
			_ = rec.Message(ctx, gone)
			goneNoteAdded = true
		}
		stepSpecs := specs
		if toolsGone {
			if forcingPlan {
				stepSpecs = planOnlySpecs(specs)
				opts.ToolChoice = "propose_plan"
			} else if !cfg.RequirePlan {
				stepSpecs = nil
			}
		} else if forceExport && planApproved && !exportedDoc {
			stepSpecs = exportOnlySpecs(specs)
			opts.ToolChoice = "export_doc"
		} else if forceMutating && planApproved {
			stepSpecs = mutatingSpecs(specs)
			opts.ToolChoice = "required"
		}
		if forcePropose && cfg.RequirePlan && !planApproved && !toolsGone {
			forcePropose = false
			stepSpecs = planOnlySpecs(specs)
			if len(stepSpecs) == 0 {
				stepSpecs = specs
			}
			opts.ToolChoice = "propose_plan"
			if cfg.ThinkingPolicy == ThinkPlanStep {
				think = true
				opts.Thinking = true
				label = "thinking"
			}
		} else if forcePropose {
			forcePropose = false
		}
		_, maxTok, elide, err := cfg.prepCall(msgs, stepSpecs, &readSpans, seenRead)
		if err != nil {
			return err
		}
		if cfg.Trace && elide.Count > 0 {
			fmt.Fprintln(cfg.Err, FormatElide(elide, cfg.Client.ContextWindow()))
		}
		opts.MaxTokens = maxTok
		live := ui.StartLive(label)
		opts.OnProgress = func(p llm.Progress) {
			live.SetLabel(ProgressLabel(label, p))
		}
		t0 := time.Now()
		applyThinkBudget(&opts, cfg.thinkingBudget())
		msg, usage, err := cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
		modelElapsed := time.Since(t0)
		live.Stop()
		if errors.Is(err, llm.ErrTruncated) {
			msgs = aggressiveTrim(msgs, userIdx)
			userIdx = lastUserTask(msgs, task)
			est := cfg.Client.EstimatePromptTokens(msgs, stepSpecs)
			window := cfg.Client.ContextWindow()
			room := window - est - contextSafety
			if room < minOutputTokens {
				return ErrContextOverflow
			}
			opts.MaxTokens = room
			live = ui.StartLive(label)
			opts.OnProgress = func(p llm.Progress) {
				live.SetLabel(ProgressLabel(label, p))
			}
			t0 = time.Now()
			applyThinkBudget(&opts, cfg.thinkingBudget())
			msg, usage, err = cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
			modelElapsed += time.Since(t0)
			live.Stop()
			if errors.Is(err, llm.ErrTruncated) {
				return ErrContextOverflow
			}
		}
		if err != nil {
			return err
		}
		if cfg.ThinkingPolicy == ThinkPlanStep && !think && !planApproved && hasProposePlan(msg) && proposePlanValid(cfg.Tools, msg) {
			if cfg.Trace {
				st.Steps++
				st.ModelTime += modelElapsed
				st.PromptTokens += usage.PromptTokens
				st.CompletionTokens += usage.CompletionTokens
				st.ReasoningTokens += usage.ReasoningTokens
				if usage.PromptTokens > 0 {
					st.LastPromptTokens = usage.PromptTokens
				}
				dumpTrace(cfg.SessionID, st.Steps, msg)
				PrintTrace(cfg.Err, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens, cfg.Client.ContextWindow(), FormatTraceCalls(msg.ToolCalls))
			} else {
				st.Steps++
				st.ModelTime += modelElapsed
				st.PromptTokens += usage.PromptTokens
				st.CompletionTokens += usage.CompletionTokens
				st.ReasoningTokens += usage.ReasoningTokens
				if usage.PromptTokens > 0 {
					st.LastPromptTokens = usage.PromptTokens
				}
			}
			think = true
			opts.Thinking = true
			opts.ToolChoice = "propose_plan"
			stepSpecs = planOnlySpecs(specs)
			if len(stepSpecs) == 0 {
				stepSpecs = specs
			}
			label = "thinking"
			_, maxTok, elide, err = cfg.prepCall(msgs, stepSpecs, &readSpans, seenRead)
			if err != nil {
				return err
			}
			if cfg.Trace && elide.Count > 0 {
				fmt.Fprintln(cfg.Err, FormatElide(elide, cfg.Client.ContextWindow()))
			}
			opts.MaxTokens = maxTok
			live = ui.StartLive(label)
			opts.OnProgress = func(p llm.Progress) {
				live.SetLabel(ProgressLabel(label, p))
			}
			t0 := time.Now()
			applyThinkBudget(&opts, cfg.thinkingBudget())
			msg, usage, err = cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
			modelElapsed = time.Since(t0)
			live.Stop()
			if errors.Is(err, llm.ErrTruncated) {
				msgs = aggressiveTrim(msgs, userIdx)
				userIdx = lastUserTask(msgs, task)
				est := cfg.Client.EstimatePromptTokens(msgs, stepSpecs)
				window := cfg.Client.ContextWindow()
				room := window - est - contextSafety
				if room < minOutputTokens {
					return ErrContextOverflow
				}
				opts.MaxTokens = room
				live = ui.StartLive(label)
				opts.OnProgress = func(p llm.Progress) {
					live.SetLabel(ProgressLabel(label, p))
				}
				t0 = time.Now()
				applyThinkBudget(&opts, cfg.thinkingBudget())
				msg, usage, err = cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
				modelElapsed += time.Since(t0)
				live.Stop()
				if errors.Is(err, llm.ErrTruncated) {
					return ErrContextOverflow
				}
			}
			if err != nil {
				return err
			}
		}
		if forcingPlan {
			msg.Content = ""
		}
		st.Steps++
		st.ModelTime += modelElapsed
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		st.ReasoningTokens += usage.ReasoningTokens
		if usage.PromptTokens > 0 {
			st.LastPromptTokens = usage.PromptTokens
		}
		if cfg.Trace {
			dumpTrace(cfg.SessionID, st.Steps, msg)
			PrintTrace(cfg.Err, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens, cfg.Client.ContextWindow(), FormatTraceCalls(msg.ToolCalls))
		}
		narration := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(narration) != "" && len(msg.ToolCalls) > 0 && !hasFinishCall(msg) {
			ui.BeforeOutput()
			if printedBlock {
				fmt.Fprintln(cfg.Out)
			}
			text := ui.CollapseBlankLines(narration)
			fmt.Fprint(cfg.Out, ui.Current().Muted(ui.Indent(text, 2)))
			if !strings.HasSuffix(text, "\n") {
				fmt.Fprintln(cfg.Out)
			}
			printedBlock = true
		}
		if len(msg.ToolCalls) > 0 {
			msg.Content = truncateNarration(msg.Content)
		}
		msgs = append(msgs, msg)
		_ = rec.Message(ctx, msg)

		if len(msg.ToolCalls) == 0 {
			if cfg.Task == "docs" && planApproved && !wroteFile && !exportedDoc && !docsNudged {
				docsNudged = true
				note := DocsFileNote
				if wantsDocExport(task, planFiles) {
					note = DocsExportNote
				}
				nudge := llm.Message{Role: llm.RoleUser, Content: note}
				msgs = append(msgs, nudge)
				_ = rec.Message(ctx, nudge)
				continue
			}
			text := trimLeadingBlanks(msg.Content)
			if strings.TrimSpace(text) == "" {
				return ErrNoAnswer
			}
			if printedBlock {
				fmt.Fprintln(cfg.Out)
			}
			writeAnswer(cfg.Out, text, cfg.Plain)
			for _, claim := range UnverifiedClaims(text, verified) {
				fmt.Fprintln(cfg.Out, ui.Current().Warn("· not verified: "+claim))
			}
			return nil
		}

		noteWasSent := noteSent
		stepReadOnly := 0
		stepOther := 0
		lastReadOnly := -1
		hadLiveOut := false
		for _, call := range msg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			tool, ok := cfg.Tools.Get(call.Function.Name)
			args := json.RawMessage(call.Function.Arguments)
			if !ok {
				prepRejectKey = ""
				prepRejects = 0
				reason := "unknown tool " + call.Function.Name
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + reason,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				continue
			}
			action, err := tool.Prepare(call.Function.Arguments)
			if err != nil {
				if errors.Is(err, tools.ErrLookupMutating) {
					skipSummary = true
					return err
				}
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, err.Error())
				content := "error: " + err.Error()
				failKey := call.Function.Name + "\x00" + call.Function.Arguments
				if failKey == prepRejectKey {
					prepRejects++
				} else {
					prepRejectKey = failKey
					prepRejects = 1
				}
				if _, dup := seenFail[failKey]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					seenFail[failKey] = struct{}{}
				}
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    content,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				if prepRejects >= identicalRejectLimit(cfg) {
					if cfg.Out != nil {
						ui.BeforeOutput()
						fmt.Fprintln(cfg.Out, err.Error())
					}
					return nil
				}
				continue
			}
			prepRejectKey = ""
			prepRejects = 0
			kind := action.Kind()
			if kind == tools.Mutating && cfg.RequirePlan && !planApproved {
				reason := "call propose_plan first"
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + reason,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "gate_blocked", IsError: true, Output: tm.Content})
				// A gate reject is not an explore step. Only the first one forces propose_plan.
				if !sawGateReject {
					sawGateReject = true
					forcePropose = true
				}
				continue
			}
			if kind == tools.ReadOnly {
				stepReadOnly++
			} else if kind != tools.Plan {
				stepOther++
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
					okHit, hits := safety.MatchPlan(safety.Plan{Files: planFiles, Commands: planCmds, Text: planText}, c)
					if cfg.Trace {
						for _, h := range hits {
							mark := "✗"
							if h.OK {
								mark = "✓"
							}
							fmt.Fprintln(cfg.Err, h.Rule+" "+mark)
						}
					}
					if okHit {
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
			if outside && cfg.Approve == "in_plan" && kind == tools.Mutating {
				reason := "outside the plan"
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				tm := llm.Message{Role: llm.RoleTool, Content: "error: " + reason, ToolCallID: call.ID}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "rejected", IsError: true, Output: tm.Content})
				continue
			}
			if !inPlanHit && kind != tools.ReadOnly && kind != tools.Final {
				_, isCmd := commandOf(action)
				if outside && approvedAll && isCmd && !cfg.AutoApprove {
					summary += " " + ui.Current().Muted("(outside plan, approved by a)")
					decision = "approved_all"
				} else {
					var d ui.Decision
					var reason string
					var err error
					forceAsk := outside && !cfg.AutoApprove
					if outside && cfg.Approve == "all_safe" {
						if _, ok := filePathOf(action); ok {
							forceAsk = true
						}
					}
					if forceAsk {
						ui.BeforeOutput()
						d, reason, err = cfg.Confirmer.ConfirmForced(summary, action.Preview())
					} else {
						if outside && cfg.AutoApprove {
							ui.BeforeOutput()
							fmt.Fprintln(cfg.Out, ui.Current().Warn(ui.CallIndent+summary))
						}
						ui.BeforeOutput()
						d, reason, err = cfg.Confirmer.Confirm(summary, action.Preview())
					}
					if err != nil {
						return err
					}
					switch d {
					case ui.Reject:
						if kind == tools.Plan {
							planRejected = true
						}
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
							planRejected = false
							planFiles = planFilesOf(action)
							planCmds = planCmdsOf(action)
							planText = planTextOf(action)
							if cfg.Guard != nil {
								cfg.Guard.AllowPlanFiles(planFiles)
							}
						}
					case ui.ApproveAll:
						decision = "approved_all"
						approvedAll = true
						if cfg.AutoApprove {
							decision = "auto"
						}
						if kind == tools.Plan {
							planApproved = true
							planRejected = false
							planFiles = planFilesOf(action)
							planCmds = planCmdsOf(action)
							planText = planTextOf(action)
							if cfg.Guard != nil {
								cfg.Guard.AllowPlanFiles(planFiles)
							}
						}
					}
				}
			}
			finish := action.Kind() == tools.Final
			if call.Function.Name == "read_file" {
				if skipped, tm := skipCoveredRead(action, call, readSpans, msgs, cfg.Out); skipped {
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					lastReadOnly = len(msgs) - 1
					continue
				}
			}
			if kind == tools.ReadOnly && call.Function.Name != "read_file" {
				key := call.Function.Name + "\x00" + canonicalJSON(call.Function.Arguments)
				if _, dup := seenRead[key]; dup {
					if !finish {
						if hadLiveOut {
							fmt.Fprintln(cfg.Out)
						}
						ui.BeforeOutput()
						fmt.Fprintln(cfg.Out, ui.CallIndent+ui.FormatToolSummary(summary+" (duplicate, skipped)"))
						printedBlock = true
					}
					tm := llm.Message{
						Role: llm.RoleTool, Content: "note: you already made this exact call; its result is above.",
						ToolCallID: call.ID,
					}
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					lastReadOnly = len(msgs) - 1
					continue
				}
				seenRead[key] = struct{}{}
			}
			if !finish {
				if hadLiveOut {
					fmt.Fprintln(cfg.Out)
				}
				ui.BeforeOutput()
				fmt.Fprintln(cfg.Out, ui.CallIndent+ui.FormatToolSummary(summary))
				printedBlock = true
			}
			start := time.Now()
			result, err := action.Run(ctx)
			elapsed := time.Since(start)
			st.ToolTime += elapsed
			if err != nil {
				if ctx.Err() != nil {
					return err
				}
				result = tools.Result{Output: err.Error(), IsError: true, Meta: err.Error()}
			}
			if result.Final {
				result.Output = longerText(narration, result.Output)
			}
			if call.Function.Name == "run_shell" && cfg.OnShell != nil {
				cfg.OnShell(result.Output)
			}
			_ = rec.Action(ctx, ActionRecord{
				Tool: call.Function.Name, Args: args, Kind: kind, Decision: decision,
				IsError: result.IsError, Meta: result.Meta, Duration: elapsed, Output: result.Output,
			})
			if result.Change != nil {
				_ = rec.FileChange(ctx, *result.Change)
			}
			if !result.IsError {
				verified = append(verified, VerifiedCall{
					Tool: call.Function.Name, Args: call.Function.Arguments, Out: result.Output,
				})
				switch call.Function.Name {
				case "write_file":
					wroteFile = true
				case "export_doc":
					exportedDoc = true
				case "read_file":
					if p, ok := action.(interface{ ResolvedPath() string }); ok {
						if hash, err := fileSHA256(p.ResolvedPath()); err == nil {
							start, end, okHdr := parseReadHeader(result.Output)
							if !okHdr {
								start, end = parseReadRange(call.Function.Arguments)
								if end < 1 {
									end = fileLineCount(p.ResolvedPath())
								}
							}
							if start > 0 && end >= start {
								readSpans = append(readSpans, readSpan{
									abs: p.ResolvedPath(), hash: hash,
									start: start, end: end, step: st.Steps, toolID: call.ID,
								})
							}
						}
					}
				}
			}
			if result.Final {
				text := trimLeadingBlanks(result.Output)
				if strings.TrimSpace(text) == "" {
					return ErrNoAnswer
				}
				if printedBlock {
					fmt.Fprintln(cfg.Out)
				}
				writeAnswer(cfg.Out, text, cfg.Plain)
				for _, claim := range UnverifiedClaims(text, verified) {
					fmt.Fprintln(cfg.Out, ui.Current().Warn("· not verified: "+claim))
				}
				return nil
			}
			pal := ui.Current()
			mark := pal.Success("✓")
			if result.IsError {
				mark = pal.Error("✗")
			}
			fmt.Fprintf(cfg.Out, "%s%s %s · %s\n", ui.OutIndent, mark, pal.Muted(displayMeta(result)), pal.Muted(formatDuration(elapsed)))
			hadLiveOut = toolPrintedOutput(call.Function.Name, result)
			content := result.Output
			if cfg.Task == "analysis" && analysisSourceRead(call.Function.Name, call.Function.Arguments) && !result.IsError {
				sourceReads++
			}
			if result.IsError {
				consecFail++
				sum := sha256.Sum256([]byte(result.Output))
				key := call.Function.Name + call.Function.Arguments + string(sum[:])
				if _, dup := seenFail[key]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					seenFail[key] = struct{}{}
				}
			} else {
				consecFail = 0
			}
			tm := llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: call.ID,
			}
			msgs = append(msgs, tm)
			_ = rec.Message(ctx, tm)
			if kind == tools.ReadOnly {
				lastReadOnly = len(msgs) - 1
			}
			if call.Function.Name == "run_shell" && result.IsError {
				shellFail = true
			}
		}
		if planApproved && (cfg.RequirePlan || cfg.Intent == "act" || cfg.Intent == "build") {
			if stepReadOnly > 0 && stepOther == 0 {
				postPlanReads++
				if cfg.Task == "docs" && !wroteFile && postPlanReads == postPlanDocsAt && !docsWriteNoted {
					docsWriteNoted = true
					nudge := llm.Message{Role: llm.RoleUser, Content: docsPlannedWriteNote(planFiles)}
					msgs = append(msgs, nudge)
					_ = rec.Message(ctx, nudge)
				}
				if postPlanReads == postPlanNoteAt {
					nudge := llm.Message{Role: llm.RoleUser, Content: PostPlanNote}
					msgs = append(msgs, nudge)
					_ = rec.Message(ctx, nudge)
				}
				if postPlanReads >= postPlanForceAt {
					forceMutating = true
				}
			} else if stepOther > 0 {
				postPlanReads = 0
				forceMutating = false
			}
		}
		if cfg.Task == "docs" && wroteFile && !exportedDoc {
			docsExportWait++
			if docsExportWait == docsExportNoteAt && !docsExportNoted {
				docsExportNoted = true
				nudge := llm.Message{Role: llm.RoleUser, Content: DocsExportNote}
				msgs = append(msgs, nudge)
				_ = rec.Message(ctx, nudge)
			}
			if docsExportWait >= docsExportForceAt {
				forceExport = true
			}
		} else if exportedDoc {
			docsExportWait = 0
			forceExport = false
		}
		prePlanEvery := !planApproved && (cfg.RequirePlan || cfg.Intent == "act" || cfg.Intent == "build")
		if prePlanEvery && (stepReadOnly > 0 || stepOther > 0) {
			explore++
			if cfg.ExploreBudget > 0 && explore >= cfg.ExploreBudget && cfg.ExploreNote != "" && lastReadOnly >= 0 {
				msgs[lastReadOnly].Content += "\n\n" + exploreNote(cfg, sourceReads)
				noteSent = true
			}
			if noteWasSent {
				readonlyAfterNote++
				if readonlyAfterNote >= hardStopAfter(cfg, sourceReads) {
					toolsGone = true
				}
			}
		} else if stepOther > 0 {
			explore = 0
		} else if stepReadOnly > 0 && (!cfg.RequirePlan || !planApproved) {
			explore++
			if cfg.ExploreBudget > 0 && explore >= cfg.ExploreBudget && cfg.ExploreNote != "" && lastReadOnly >= 0 {
				msgs[lastReadOnly].Content += "\n\n" + exploreNote(cfg, sourceReads)
				noteSent = true
			}
			if noteWasSent {
				readonlyAfterNote++
				if readonlyAfterNote >= hardStopAfter(cfg, sourceReads) {
					toolsGone = true
				}
			}
		}
	}
	note := llm.Message{Role: llm.RoleUser, Content: MaxStepsNote}
	msgs = append(msgs, note)
	_ = rec.Message(ctx, note)
	_, maxTok, elide, prepErr := PrepareCall(cfg.Client, msgs, nil, cfg.Intent)
	if prepErr != nil {
		return prepErr
	}
	if cfg.Trace && elide.Count > 0 {
		fmt.Fprintln(cfg.Err, FormatElide(elide, cfg.Client.ContextWindow()))
	}
	live := ui.StartLive("thinking")
	t0 := time.Now()
	msg, usage, err := cfg.Client.Complete(ctx, msgs, nil, llm.ChatOptions{
		Thinking: false, Temperature: temp, MaxTokens: maxTok,
		OnProgress: func(p llm.Progress) { live.SetLabel(ProgressLabel("thinking", p)) },
	})
	live.Stop()
	if err != nil {
		if cfg.Trace {
			fmt.Fprintln(cfg.Err, "max-steps summary:", err)
		}
	} else {
		st.Steps++
		st.ModelTime += time.Since(t0)
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		if usage.PromptTokens > 0 {
			st.LastPromptTokens = usage.PromptTokens
		}
		text := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(text) != "" {
			if printedBlock {
				fmt.Fprintln(cfg.Out)
			}
			writeAnswer(cfg.Out, text, cfg.Plain)
		}
	}
	return ErrMaxSteps
}
