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
	NamedRoots        []string
	TokenCap          int
	ThinkingMaxTokens int
	PlanThink         string
	ElideTrigger      float64
	ElideTarget       float64
	IdenticalRejects  int
	Pricing           Pricing
	Serving           Model
	// ToolChoiceMode is named, required or auto for a forced tool. Empty is
	// named, the local-server default.
	ToolChoiceMode string
	// ReasoningHeadroom is added to the output cap for a reasoning model, so
	// its reasoning tokens do not consume the whole answer budget.
	ReasoningHeadroom int
	// AnswerPanel frames the final answer on a TTY; JSONMaxLines caps JSON.
	AnswerPanel  bool
	JSONMaxLines int
}

// Pricing is the serving model's location and its prices per million tokens.
// A local model or two unset prices keep the turn's cost line off.
type Pricing struct {
	Remote   bool
	PriceIn  float64
	PriceOut float64
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
	Rejects          int
	OutsidePlan      int
	MCPCalls         int
	MaxStep          time.Duration
	Pricing          Pricing
	Serving          Model
}

func (s *Stats) noteModel(d time.Duration) {
	s.ModelTime += d
	if d > s.MaxStep {
		s.MaxStep = d
	}
}

var (
	ErrAborted         = errors.New("agent: aborted")
	ErrMaxSteps        = errors.New("agent: max steps exceeded")
	ErrNoAnswer        = errors.New("agent: no answer")
	ErrNoPlan          = errors.New("agent: the model did not call propose_plan after the nudge")
	ErrRejected        = errors.New("agent: the same rejected call was repeated")
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

// runState holds the flags, counters and slices that the steps of one Run
// share. Run keeps the control flow; the struct only gives the state a name.
type runState struct {
	seenFail          map[string]struct{}
	seenRead          map[string]struct{}
	prepRejectKey     string
	prepRejects       int
	userRejectKey     string
	userRejects       int
	readSpans         []readSpan
	planApproved      bool
	planFiles         []string
	planTools         []string
	planCmds          []string
	planText          string
	explore           int
	noteSent          bool
	forcePropose      bool
	planNudged        bool
	changeNudged      bool
	sawGateReject     bool
	readonlyAfterNote int
	sourceReads       int
	toolsGone         bool
	goneNoteAdded     bool
	wroteFile         bool
	verified          []VerifiedCall
	exportedDoc       bool
	docsNudged        bool
	docsWriteNoted    bool
	docsExportNoted   bool
	docsExportWait    int
	postPlanReads     int
	forceMutating     bool
	forceExport       bool
	shellFail         bool
	planRejected      bool
	approvedAll       bool
	printedBlock      bool
	consecFail        int
	skipSummary       bool
}

// toolChoiceFor is the tool_choice value for a forced tool call. Auto mode
// omits the field and relies on the prompt plus the plan nudge; required asks
// for any tool; named asks for one function.
func (cfg Config) toolChoiceFor(name string) string {
	switch strings.ToLower(strings.TrimSpace(cfg.ToolChoiceMode)) {
	case "auto":
		return "auto"
	case "required":
		return "required"
	}
	if name == "" {
		return "required"
	}
	return name
}

// planNudgeNeeded reports whether a forced plan has to be enforced by a
// nudge: auto and required modes cannot name the tool, and a client that
// rejected tool_choice has been downgraded to auto.
func (cfg Config) planNudgeNeeded() bool {
	switch strings.ToLower(strings.TrimSpace(cfg.ToolChoiceMode)) {
	case "auto", "required":
		return true
	}
	return cfg.Client != nil && cfg.Client.ForcedToolChoiceDisabled()
}

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
	// If the step ends early (interrupt, abort, rejected, final call), the last
	// assistant tool_calls message may not have all its replies. Record them so
	// the stored history stays valid for a resume. The closure reads the final
	// msgs value, which grows as the loop appends.
	defer func() { RecordMissingToolReplies(ctx, rec, msgs) }()
	specs := cfg.Tools.Specs()
	state := &runState{
		seenFail:     make(map[string]struct{}),
		seenRead:     make(map[string]struct{}),
		planApproved: cfg.ResumePlan,
		planFiles:    cfg.PlanFiles,
		planCmds:     cfg.PlanCmds,
		planText:     cfg.PlanText,
	}
	if state.planApproved && cfg.Guard != nil {
		cfg.Guard.AllowPlanFiles(state.planFiles)
	}
	st := Stats{Pricing: cfg.Pricing, Serving: cfg.Serving}
	st.RouterTime = cfg.RouterTime
	defer func() {
		if cfg.Stats != nil {
			*cfg.Stats = st
		}
		if cfg.Trace && cfg.Err != nil {
			fmt.Fprintln(cfg.Err, FormatStatsLine(st))
		}
		if state.skipSummary || cfg.HideStats {
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

	for step := 0; step < analysisLimit(cfg, state.sourceReads); step++ {
		think := cfg.ThinkingPolicy.enabled(state.planApproved, state.shellFail, state.consecFail)
		if cfg.ThinkingPolicy == ThinkPlanStep && state.planRejected && !state.planApproved {
			think = true
		}
		opts := llm.ChatOptions{Thinking: think, Temperature: temp, TopP: cfg.TopP}
		state.shellFail = false
		forcingPlan := state.toolsGone && cfg.RequirePlan && !state.planApproved
		if forcingPlan && cfg.ThinkingPolicy == ThinkPlanStep {
			think = true
			opts.Thinking = true
		}
		label := "working"
		if cfg.RequirePlan && !state.planApproved {
			label = "planning"
		} else if think {
			label = "thinking"
		}
		if state.toolsGone && !state.goneNoteAdded {
			note := ToolsGoneNote
			if forcingPlan {
				note = ForcedPlanNote
			}
			gone := llm.Message{Role: llm.RoleUser, Content: note}
			msgs = append(msgs, gone)
			_ = rec.Message(ctx, gone)
			state.goneNoteAdded = true
		}
		stepSpecs := specs
		if cfg.RequirePlan && !state.planApproved && !state.toolsGone && !state.forcePropose && !state.planNudged {
			// Before the plan, only read-only exploration and propose_plan are
			// offered: a mutating call cannot be wasted on "call propose_plan first".
			stepSpecs = prePlanSpecs(specs)
		}
		planForced := false
		if state.toolsGone {
			if forcingPlan {
				stepSpecs = planOnlySpecs(specs)
				opts.ToolChoice = cfg.toolChoiceFor("propose_plan")
				planForced = true
			} else if !cfg.RequirePlan {
				stepSpecs = nil
			}
		} else if state.forceExport && state.planApproved && !state.exportedDoc {
			stepSpecs = exportOnlySpecs(specs)
			opts.ToolChoice = cfg.toolChoiceFor("export_doc")
		} else if state.forceMutating && state.planApproved {
			stepSpecs = mutatingSpecs(specs)
			opts.ToolChoice = cfg.toolChoiceFor("")
		}
		if state.forcePropose && cfg.RequirePlan && !state.planApproved && !state.toolsGone {
			state.forcePropose = false
			stepSpecs = planOnlySpecs(specs)
			if len(stepSpecs) == 0 {
				stepSpecs = specs
			}
			opts.ToolChoice = cfg.toolChoiceFor("propose_plan")
			planForced = true
			if cfg.ThinkingPolicy == ThinkPlanStep {
				think = true
				opts.Thinking = true
				label = "thinking"
			}
		} else if state.forcePropose {
			state.forcePropose = false
		}
		if state.planNudged && cfg.RequirePlan && !state.planApproved {
			// The nudge retry is still a forced plan step, so the same enforcement
			// applies if the model ignores it again.
			planForced = true
			stepSpecs = planOnlySpecs(specs)
			if len(stepSpecs) == 0 {
				stepSpecs = specs
			}
			opts.ToolChoice = cfg.toolChoiceFor("propose_plan")
		}
		_, maxTok, elide, err := cfg.prepCall(msgs, stepSpecs, &state.readSpans, state.seenRead)
		maxTok += cfg.ReasoningHeadroom
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
		if cfg.Trace && cfg.Client != nil && cfg.Client.LastReasoningFallback() != "" {
			fmt.Fprintf(cfg.Err, "reasoning_effort not supported with tools; using %s for this model (retried once)\n", cfg.Client.LastReasoningFallback())
		}
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
		if planForced && cfg.planNudgeNeeded() && !hasProposePlan(msg) {
			// A forced plan must actually arrive. Auto/required modes cannot use a
			// named tool_choice, so ask once more and then fail clearly.
			st.Steps++
			st.noteModel(modelElapsed)
			st.PromptTokens += usage.PromptTokens
			st.CompletionTokens += usage.CompletionTokens
			st.ReasoningTokens += usage.ReasoningTokens
			msgs = append(msgs, msg)
			_ = rec.Message(ctx, msg)
			for _, call := range msg.ToolCalls {
				tm := llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: "not executed: propose_plan must be called first"}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
			}
			if state.planNudged {
				return ErrNoPlan
			}
			state.planNudged = true
			nudge := llm.Message{Role: llm.RoleUser, Content: ForcedPlanNote}
			msgs = append(msgs, nudge)
			_ = rec.Message(ctx, nudge)
			continue
		}
		if cfg.ThinkingPolicy == ThinkPlanStep && !think && !state.planApproved && hasProposePlan(msg) && (!proposePlanValid(cfg.Tools, msg) || planThinkAlways(cfg.PlanThink)) {
			if cfg.Trace {
				st.Steps++
				st.noteModel(modelElapsed)
				st.PromptTokens += usage.PromptTokens
				st.CompletionTokens += usage.CompletionTokens
				st.ReasoningTokens += usage.ReasoningTokens
				if usage.PromptTokens > 0 {
					st.LastPromptTokens = usage.PromptTokens
				}
				dumpTrace(cfg.SessionID, st.Steps, msg)
				PrintTraceServing(cfg.Err, cfg.Serving, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens, cfg.Client.ContextWindow(), FormatTraceCalls(msg.ToolCalls))
			} else {
				st.Steps++
				st.noteModel(modelElapsed)
				st.PromptTokens += usage.PromptTokens
				st.CompletionTokens += usage.CompletionTokens
				st.ReasoningTokens += usage.ReasoningTokens
				if usage.PromptTokens > 0 {
					st.LastPromptTokens = usage.PromptTokens
				}
			}
			think = true
			opts.Thinking = true
			opts.ToolChoice = cfg.toolChoiceFor("propose_plan")
			stepSpecs = planOnlySpecs(specs)
			if len(stepSpecs) == 0 {
				stepSpecs = specs
			}
			label = "thinking"
			_, maxTok, elide, err = cfg.prepCall(msgs, stepSpecs, &state.readSpans, state.seenRead)
			maxTok += cfg.ReasoningHeadroom
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
		st.noteModel(modelElapsed)
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		st.ReasoningTokens += usage.ReasoningTokens
		if usage.PromptTokens > 0 {
			st.LastPromptTokens = usage.PromptTokens
		}
		if cfg.Trace {
			dumpTrace(cfg.SessionID, st.Steps, msg)
			PrintTraceServing(cfg.Err, cfg.Serving, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, usage.ReasoningTokens, cfg.Client.ContextWindow(), FormatTraceCalls(msg.ToolCalls))
		}
		narration := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(narration) != "" && len(msg.ToolCalls) > 0 && !hasFinishCall(msg) {
			ui.BeforeOutput()
			if state.printedBlock {
				fmt.Fprintln(cfg.Out)
			}
			text := ui.CollapseBlankLines(narration)
			fmt.Fprint(cfg.Out, ui.Current().Muted(ui.Indent(text, 2)))
			if !strings.HasSuffix(text, "\n") {
				fmt.Fprintln(cfg.Out)
			}
			state.printedBlock = true
		}
		if len(msg.ToolCalls) > 0 {
			msg.Content = truncateNarration(msg.Content)
		}
		msgs = append(msgs, msg)
		_ = rec.Message(ctx, msg)

		if len(msg.ToolCalls) == 0 {
			if cfg.RequirePlan && !state.planApproved && !state.changeNudged && !state.sawGateReject && (cfg.Intent == "act" || cfg.Intent == "build") {
				// A change request answered with prose: the mutating tools are
				// locked behind the plan, so nudge once to call propose_plan.
				state.changeNudged = true
				nudge := llm.Message{Role: llm.RoleUser, Content: PlanUnlockNote}
				msgs = append(msgs, nudge)
				_ = rec.Message(ctx, nudge)
				continue
			}
			if cfg.Task == "docs" && state.planApproved && !state.wroteFile && !state.exportedDoc && !state.docsNudged {
				state.docsNudged = true
				note := DocsFileNote
				if wantsDocExport(task, state.planFiles) {
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
			if state.printedBlock {
				fmt.Fprintln(cfg.Out)
			}
			writeFinalAnswer(cfg, cfg.Out, text, cfg.Plain)
			for _, claim := range UnverifiedClaims(text, state.verified) {
				fmt.Fprintln(cfg.Out, ui.Current().Warn("· not verified: "+claim))
			}
			return nil
		}

		noteWasSent := state.noteSent
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
				state.prepRejectKey = ""
				state.prepRejects = 0
				reason := "unknown tool " + call.Function.Name
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				st.Rejects++
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
					state.skipSummary = true
					return err
				}
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, err.Error())
				st.Rejects++
				content := "error: " + err.Error()
				failKey := call.Function.Name + "\x00" + call.Function.Arguments
				if failKey == state.prepRejectKey {
					state.prepRejects++
				} else {
					state.prepRejectKey = failKey
					state.prepRejects = 1
				}
				if _, dup := state.seenFail[failKey]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					state.seenFail[failKey] = struct{}{}
				}
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    content,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				if state.prepRejects >= identicalRejectLimit(cfg) {
					return fmt.Errorf("%w: %s, %d times", ErrRejected, call.Function.Name, state.prepRejects)
				}
				continue
			}
			state.prepRejectKey = ""
			state.prepRejects = 0
			kind := action.Kind()
			if kind == tools.Mutating && cfg.RequirePlan && !state.planApproved {
				reason := "call propose_plan first"
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				st.Rejects++
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + reason,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "gate_blocked", IsError: true, Output: tm.Content})
				// A gate reject is not an state.explore step. Only the first one forces propose_plan.
				if !state.sawGateReject {
					state.sawGateReject = true
					state.forcePropose = true
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
			if kind == tools.Mutating && cfg.RequirePlan && state.planApproved {
				if server, tool, ok := mcpIdentity(action); ok {
					if mcpInPlan(state.planTools, call.Function.Name, server, tool) {
						inPlanHit = true
					} else {
						outside = true
					}
				} else if p, ok := filePathOf(action); ok {
					planRoot, planBase := "", ""
					if cfg.Guard != nil {
						planRoot, planBase = cfg.Guard.Root(), cfg.Guard.Base()
					}
					if inPlanPath(state.planFiles, p, planRoot, planBase) {
						inPlanHit = true
					} else {
						outside = true
					}
				} else if c, ok := commandOf(action); ok {
					okHit, hits := safety.MatchPlan(safety.Plan{Files: state.planFiles, Commands: state.planCmds, Text: state.planText}, c)
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
					st.OutsidePlan++
				}
			}
			if inPlanHit {
				summary += " " + ui.Current().Muted("(in plan)")
			}
			if outside && cfg.Approve == "in_plan" && kind == tools.Mutating {
				reason := "outside the plan"
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				st.Rejects++
				tm := llm.Message{Role: llm.RoleTool, Content: "error: " + reason, ToolCallID: call.ID}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "rejected", IsError: true, Output: tm.Content})
				continue
			}
			if !inPlanHit && kind != tools.ReadOnly && kind != tools.Final {
				_, isCmd := commandOf(action)
				if outside && state.approvedAll && isCmd && !cfg.AutoApprove {
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
						d, reason, err = cfg.Confirmer.ConfirmForced(summary, namedPreview(action.Preview(), cfg.NamedRoots))
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
						st.Rejects++
						if kind == tools.Plan {
							state.planRejected = true
						}
						tm := llm.Message{
							Role:       llm.RoleTool,
							Content:    "user rejected: " + reason,
							ToolCallID: call.ID,
						}
						msgs = append(msgs, tm)
						_ = rec.Message(ctx, tm)
						_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "rejected", RejectReason: reason, IsError: true, Output: tm.Content})
						rejectKey := call.Function.Name + "\x00" + call.Function.Arguments
						if rejectKey == state.userRejectKey {
							state.userRejects++
						} else {
							state.userRejectKey, state.userRejects = rejectKey, 1
						}
						if state.userRejects >= identicalRejectLimit(cfg) {
							return fmt.Errorf("%w: %s, rejected %d times", ErrRejected, call.Function.Name, state.userRejects)
						}
						continue
					case ui.Quit:
						return ErrAborted
					case ui.Approve:
						state.userRejectKey, state.userRejects = "", 0
						decision = "approved"
						if cfg.AutoApprove {
							decision = "auto"
						}
						if kind == tools.Plan {
							state.planApproved = true
							state.planNudged = false
							state.planRejected = false
							state.planFiles = planFilesOf(action)
							state.planCmds = planCmdsOf(action)
							state.planTools = planToolsOf(action)
							state.planText = planTextOf(action)
							if cfg.Guard != nil {
								cfg.Guard.AllowPlanFiles(state.planFiles)
							}
						}
					case ui.ApproveAll:
						decision = "approved_all"
						state.approvedAll = true
						if cfg.AutoApprove {
							decision = "auto"
						}
						if kind == tools.Plan {
							state.planApproved = true
							state.planRejected = false
							state.planFiles = planFilesOf(action)
							state.planCmds = planCmdsOf(action)
							state.planTools = planToolsOf(action)
							state.planText = planTextOf(action)
							if cfg.Guard != nil {
								cfg.Guard.AllowPlanFiles(state.planFiles)
							}
						}
					}
				}
			}
			finish := action.Kind() == tools.Final
			if call.Function.Name == "read_file" {
				if skipped, tm := skipCoveredRead(action, call, state.readSpans, msgs, cfg.Out); skipped {
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					lastReadOnly = len(msgs) - 1
					continue
				}
			}
			if kind == tools.ReadOnly && call.Function.Name != "read_file" {
				key := call.Function.Name + "\x00" + canonicalJSON(call.Function.Arguments)
				if _, dup := state.seenRead[key]; dup {
					if !finish {
						if hadLiveOut {
							fmt.Fprintln(cfg.Out)
						}
						ui.BeforeOutput()
						fmt.Fprintln(cfg.Out, ui.CallIndent+ui.FormatToolSummary(summary+" (duplicate, skipped)"))
						state.printedBlock = true
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
				state.seenRead[key] = struct{}{}
			}
			if !finish {
				if hadLiveOut {
					fmt.Fprintln(cfg.Out)
				}
				ui.BeforeOutput()
				fmt.Fprintln(cfg.Out, ui.CallIndent+ui.FormatToolSummary(summary))
				state.printedBlock = true
			}
			start := time.Now()
			if _, ok := action.(interface{ MCPServer() string }); ok {
				st.MCPCalls++
			}
			result, err := action.Run(ctx)
			elapsed := time.Since(start)
			if err == nil && !result.IsError {
				if d, ok := action.(tools.Discoverer); ok {
					if extra := d.DiscoveredTools(); len(extra) > 0 {
						// A find-tool result makes the discovered schemas callable for the
						// rest of this turn; the caller's registry is not mutated.
						cfg.Tools = cfg.Tools.Add(extra...)
						specs = cfg.Tools.Specs()
					}
				}
			}
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
				state.verified = append(state.verified, VerifiedCall{
					Tool: call.Function.Name, Args: call.Function.Arguments, Out: result.Output,
				})
				switch call.Function.Name {
				case "write_file":
					state.wroteFile = true
				case "export_doc":
					state.exportedDoc = true
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
								state.readSpans = append(state.readSpans, readSpan{
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
				if state.printedBlock {
					fmt.Fprintln(cfg.Out)
				}
				writeFinalAnswer(cfg, cfg.Out, text, cfg.Plain)
				for _, claim := range UnverifiedClaims(text, state.verified) {
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
				state.sourceReads++
			}
			if result.IsError {
				state.consecFail++
				sum := sha256.Sum256([]byte(result.Output))
				key := call.Function.Name + call.Function.Arguments + string(sum[:])
				if _, dup := state.seenFail[key]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					state.seenFail[key] = struct{}{}
				}
			} else {
				state.consecFail = 0
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
				state.shellFail = true
			}
		}
		if state.planApproved && (cfg.RequirePlan || cfg.Intent == "act" || cfg.Intent == "build") {
			if stepReadOnly > 0 && stepOther == 0 {
				state.postPlanReads++
				if cfg.Task == "docs" && !state.wroteFile && state.postPlanReads == postPlanDocsAt && !state.docsWriteNoted {
					state.docsWriteNoted = true
					nudge := llm.Message{Role: llm.RoleUser, Content: docsPlannedWriteNote(state.planFiles)}
					msgs = append(msgs, nudge)
					_ = rec.Message(ctx, nudge)
				}
				if state.postPlanReads == postPlanNoteAt {
					nudge := llm.Message{Role: llm.RoleUser, Content: PostPlanNote}
					msgs = append(msgs, nudge)
					_ = rec.Message(ctx, nudge)
				}
				if state.postPlanReads >= postPlanForceAt {
					state.forceMutating = true
				}
			} else if stepOther > 0 {
				state.postPlanReads = 0
				state.forceMutating = false
			}
		}
		if cfg.Task == "docs" && state.wroteFile && !state.exportedDoc {
			state.docsExportWait++
			if state.docsExportWait == docsExportNoteAt && !state.docsExportNoted {
				state.docsExportNoted = true
				nudge := llm.Message{Role: llm.RoleUser, Content: DocsExportNote}
				msgs = append(msgs, nudge)
				_ = rec.Message(ctx, nudge)
			}
			if state.docsExportWait >= docsExportForceAt {
				state.forceExport = true
			}
		} else if state.exportedDoc {
			state.docsExportWait = 0
			state.forceExport = false
		}
		prePlanEvery := !state.planApproved && (cfg.RequirePlan || cfg.Intent == "act" || cfg.Intent == "build")
		if prePlanEvery && (stepReadOnly > 0 || stepOther > 0) {
			state.explore++
			if cfg.ExploreBudget > 0 && state.explore >= cfg.ExploreBudget && cfg.ExploreNote != "" && lastReadOnly >= 0 {
				msgs[lastReadOnly].Content += "\n\n" + exploreNote(cfg, state.sourceReads)
				state.noteSent = true
			}
			if noteWasSent {
				state.readonlyAfterNote++
				if state.readonlyAfterNote >= hardStopAfter(cfg, state.sourceReads) {
					state.toolsGone = true
				}
			}
		} else if stepOther > 0 {
			state.explore = 0
		} else if stepReadOnly > 0 && (!cfg.RequirePlan || !state.planApproved) {
			state.explore++
			if cfg.ExploreBudget > 0 && state.explore >= cfg.ExploreBudget && cfg.ExploreNote != "" && lastReadOnly >= 0 {
				msgs[lastReadOnly].Content += "\n\n" + exploreNote(cfg, state.sourceReads)
				state.noteSent = true
			}
			if noteWasSent {
				state.readonlyAfterNote++
				if state.readonlyAfterNote >= hardStopAfter(cfg, state.sourceReads) {
					state.toolsGone = true
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
		st.noteModel(time.Since(t0))
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		if usage.PromptTokens > 0 {
			st.LastPromptTokens = usage.PromptTokens
		}
		text := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(text) != "" {
			if state.printedBlock {
				fmt.Fprintln(cfg.Out)
			}
			writeFinalAnswer(cfg, cfg.Out, text, cfg.Plain)
		}
	}
	return ErrMaxSteps
}

// namedPreview appends the write roots the request named, so the forced
// confirmation shows everything the turn will allow.
func namedPreview(preview string, roots []string) string {
	if len(roots) == 0 {
		return preview
	}
	line := "write roots named by the request: " + strings.Join(roots, ", ")
	if strings.TrimSpace(preview) == "" {
		return line
	}
	return strings.TrimRight(preview, "\n") + "\n" + line
}
