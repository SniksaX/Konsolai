package turn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/rag"
	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/sysctx"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func (r *Runner) RunTurn(ctx context.Context, req Request) Result {
	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(r.errw(), "konsolai:", err)
			return Result{ExitCode: 1}
		}
	}
	r.escalated = false
	if r.RebindNext {
		r.Client = nil
		r.FastClient = nil
		r.RebindNext = false
	}
	if r.Profile.UI.Trace {
		req.Trace = true
	}
	text := req.Text
	if extra := strings.TrimRight(req.Stdin, "\n"); extra != "" {
		if strings.TrimSpace(text) == "" {
			text = extra
		} else {
			text = text + "\n\n<stdin>\n" + extra + "\n</stdin>"
		}
	}
	if strings.TrimSpace(text) == "" {
		return Result{ExitCode: 2}
	}
	expanded, atts := ExpandMentions(ctx, cwd, text)
	if len(atts) > 0 {
		PrintAttachments(r.out(), atts)
		text = expanded
	}
	req.Text = text

	mem := r.mem()
	projectID := UpsertProject(ctx, mem, cwd)
	snap, err := sysctx.Collect()
	if err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return Result{ExitCode: 1}
	}
	r.beginStats(req.Intent)
	defer r.endStats()
	client := r.client()
	if _, err := client.ResolveModel(ctx); err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return Result{ExitCode: 1}
	}
	thinkMode := r.ThinkMode
	if thinkMode == ThinkAuto {
		switch strings.ToLower(strings.TrimSpace(r.Profile.Agent.ThinkMode)) {
		case "on":
			thinkMode = ThinkAlways
		case "off":
			thinkMode = ThinkNever
		}
	}
	if text, hard := StripThinkPrefix(req.Text); hard {
		req.Text = text
		thinkMode = ThinkAlways
	}
	facts := LoadFactsBlock(ctx, mem, r.Profile.Name, projectID)
	conf, closeConf, err := r.confirm(ctx, req.Yes)
	if err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return Result{ExitCode: 1}
	}
	defer closeConf()

	cands := FindCandidates(ctx, mem, r.Profile, projectID, cwd)
	routerCands := ToIntentCandidates(cands)
	if req.New || req.ResumeSession != "" {
		routerCands = nil
	}
	root := repo.Root(cwd)
	inRepo := false
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		inRepo = true
	}
	routedAt := time.Now()
	understand := ui.Start("understanding")
	routeClient := r.routerClient(ctx)
	dec, continued := r.continueDecision(req.Text)
	if !(continued && dec.Reason == "continue-cut") {
		r.answerCut = false
	}
	spoken := req.Text
	if !continued {
		dec = DecideIntentFloor(ctx, routeClient, req.Intent, req.Text, req.New, routerCands, inRepo, r.Profile.Router.ActionVerbs, r.Profile.Router.Targets)
	} else if dec.Reason == "continue-promise" && r.lastUser != "" {
		req.Text = r.lastUser
		r.lastPromise = false
	} else if dec.Reason == "clarification" {
		req.Text = intent.WithPreviousExchange(req.Text, r.lastUser, r.lastAssistant)
	} else {
		req.Text = strings.TrimSpace(req.Text) + "\n\n" + continueNote
	}
	understand()
	routerTime := time.Since(routedAt)
	r.setStatIntent(string(dec.Intent))
	if thinkMode == ThinkAlways {
		dec.Effort = intent.EffortHigh
	}
	if effort := intent.Effort(req.Effort); req.Effort != "" {
		switch effort {
		case intent.EffortNone, intent.EffortLow, intent.EffortHigh:
			dec.Effort = effort
		}
	}
	r.RouterModel = routeClient.ModelID()
	if r.RouterModel == "" {
		if id, err := routeClient.ResolveModel(ctx); err == nil {
			r.RouterModel = id
		}
	}
	if req.Trace {
		agent.PrintRouter(r.errw(), r.RouterModel, routerTime, string(dec.EffectiveEffort()))
	}

	var picked ResumeCandidate
	if req.ResumeSession != "" {
		picked = loadSessionByID(ctx, mem, req.ResumeSession, cands)
	} else {
		picked = ApplyResume(req.New, dec, cands)
	}
	if picked.ID != "" && len(picked.Messages) == 0 {
		picked.Messages = LoadCompacted(ctx, mem, picked.ID, r.Profile.Name, cwd)
	}
	sessionID, history := picked.ID, picked.Messages
	r.setStatSession(sessionID)
	win := client.ContextWindow()
	r.LastWindow = win
	if ShouldCompactRatio(r.LastPromptTokens, win, r.Profile.CompactThreshold()) && len(history) > compactKeep {
		before := client.EstimatePromptTokens(history, nil)
		sum, upto, cerr := CompactMessages(ctx, client, history)
		if cerr == nil && sum != "" {
			PersistCompact(ctx, mem, sessionID, r.Profile.Name, cwd, sum, upto, history)
			if r.Ingest != nil {
				r.Ingest.EnqueueSummary(projectID, sessionID, sum, upto)
			}
			history = HistoryWithSummary(history, sum, upto)
			after := client.EstimatePromptTokens(history, nil)
			PrintCompacted(r.errw(), upto, before, after, win)
		}
	}
	if len(history) > 0 {
		history = agent.LeanResume(history, client.TokensPerChar())
		if req.ResumeSession == "" {
			fmt.Fprintln(r.out(), ui.Dim("↻ continuing session: "+picked.Title))
		}
	}
	ui.BeforeOutput()
	if dec.Intent != intent.Answer && dec.Intent != intent.Lookup {
		r.emitBadge(req, string(dec.Intent), false)
	}
	if intent.LooksLikeMemory(req.Text) {
		dec.NeedsMemory = true
	}
	if n, text := r.attachSessionMemory(ctx, mem, projectID, req.Text, dec.NeedsMemory); n > 0 {
		req.Text = text
		fmt.Fprintln(r.out(), ui.Current().Muted(fmt.Sprintf("recalled %d memories", n)))
	}
	if sessionID == "" && len(history) == 0 {
		sessionID = CreateSession(ctx, mem, r.Profile, projectID, cwd, string(dec.Intent), req.Text)
	}
	r.setStatSession(sessionID)
	rec := memory.NewRecorder(mem, sessionID, cwd)
	tap := &sessionTap{inner: rec, msgs: append([]llm.Message(nil), history...)}
	if len(tap.msgs) > 20 {
		tap.msgs = tap.msgs[len(tap.msgs)-20:]
	}
	code, answer, ptok, usedMain := r.runProfile(ctx, client, snap, facts, history, tap, mem, sessionID, projectID, req, cwd, conf, &dec, routerTime, thinkMode)
	if tap.answer != "" {
		answer = tap.answer
	}
	r.LastWindow = client.ContextWindow()
	if usedMain && ptok > 0 {
		r.LastPromptTokens = ptok
	}
	r.lastCode = code
	r.lastIntent = dec.Intent
	r.lastLang = dec.Language
	r.lastEffort = dec.EffectiveEffort()
	r.lastTask = dec.Task
	r.lastUser = spoken
	r.lastAssistant = answer
	if sessionID != "" && !req.KeepOpen {
		if s := mem.Store(); s != nil {
			endCtx, cancel := mem.Isolated(ctx)
			defer cancel()
			if err := s.EndSession(endCtx, sessionID, SessionStatus(code)); err != nil {
				mem.Fail(err)
			}
		}
	}
	if code == 0 && answer != "" && r.Ingest != nil && (dec.Intent == intent.Answer || dec.Intent == intent.Lookup || dec.Intent == intent.Act || dec.Intent == intent.Build) {
		title := req.Text
		if utf8.RuneCountInString(title) > 80 {
			title = string([]rune(title)[:80])
		}
		r.Ingest.EnqueueTurn(projectID, req.Text, answer, sessionID, title, 0)
	}
	if mem.Store() == nil {
		title := req.Text
		if utf8.RuneCountInString(title) > 80 {
			title = string([]rune(title)[:80])
		}
		prev, ok := memory.LoadLast(r.Profile.Name, cwd, 0)
		if !req.New && ok && prev.Title != "" {
			title = prev.Title
		}
		sum, upto := prev.CompactSummary, prev.CompactedUpto
		_ = memory.SaveLast(memory.LastSession{
			Profile:        r.Profile.Name,
			CWD:            cwd,
			Title:          title,
			Messages:       tap.msgs,
			CompactSummary: sum,
			CompactedUpto:  upto,
		})
	}
	return Result{
		SessionID: sessionID, Intent: string(dec.Intent), ExitCode: code, Answer: answer,
		PromptTokens: ptok, Window: r.LastWindow,
	}
}

func (r *Runner) emitBadge(req Request, name string, escalated bool) {
	if r == nil || !r.Profile.ShowBadge() {
		return
	}
	if req.Plain {
		suf := ""
		if escalated {
			suf = " · escalated"
		}
		fmt.Fprintln(r.out(), "· "+name+suf)
		return
	}
	fmt.Fprintln(r.out(), ui.IntentBadgeLine(name, escalated))
}

func loadSessionByID(ctx context.Context, mem *memory.Guard, id string, cands []ResumeCandidate) ResumeCandidate {
	for _, c := range cands {
		if c.ID == id {
			if len(c.Messages) == 0 {
				c.Messages = LoadCompacted(ctx, mem, id, "", "")
			}
			return c
		}
	}
	msgs := LoadCompacted(ctx, mem, id, "", "")
	if msgs == nil {
		return ResumeCandidate{}
	}
	return ResumeCandidate{ID: id, Messages: msgs}
}

func (r *Runner) runProfile(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, facts string, history []llm.Message, rec agent.Recorder, mem *memory.Guard, sessionID string, projectID *int64, req Request, cwd string, confirmer agent.Confirmer, dec *intent.Decision, routerTime time.Duration, thinkMode ThinkMode) (int, string, int, bool) {
	if dec == nil {
		return 1, "", 0, true
	}
	switch strings.ToLower(strings.TrimSpace(r.Profile.Language.Mode)) {
	case "en", "fr":
		dec.Language = strings.ToLower(strings.TrimSpace(r.Profile.Language.Mode))
	default:
		dec.Language = intent.ResolveLanguage(req.Text, lastUserOf(history), dec.Language, r.Profile.Language.Default, r.Profile.Language.Languages)
	}
	lang := agent.ReplyInstruction(dec.Language)
	if dec.Intent == intent.Answer {
		prompt := agent.AnswerPrompt + "\n" + lang
		if r.SlashHelp != "" {
			prompt += "\nSlash commands:\n" + r.SlashHelp
		}
		answerClient := client
		if dec.EffectiveEffort() == intent.EffortNone {
			if fc := r.fastClient(); fc != nil && fc.Reachable(ctx) {
				if r.fastPromptTooLarge(ctx, fc, snap, history, req.Text, prompt) {
					if req.Trace {
						fmt.Fprintln(r.errw(), "fast model skipped (history too large)")
					}
				} else {
					answerClient = fc
				}
			}
		}
		think := answerThink(dec, r.Profile.AnswerThinking(), thinkMode)
		code, answer, ptok, used, esc := r.runAnswer(ctx, client, answerClient, snap, history, rec, req.Text, prompt, think, req, *dec, routerTime)
		if esc != "" && !r.escalated {
			r.escalated = true
			r.lastPromise = true
			r.lastUser = req.Text
			r.lastEscalate = esc
			dec.Intent = esc
			dec.Effort = intent.EffortHigh
			r.emitBadge(req, string(dec.Intent), true)
		} else {
			r.lastPromise = intent.IsPromise(answer)
			if r.lastPromise {
				r.lastUser = req.Text
				r.lastEscalate = intent.EscalateIntent(req.Text, r.Profile.Router.ActionVerbs, r.Profile.Router.Targets)
			}
			return code, answer, ptok, used
		}
	}

	root := repo.Root(cwd)
	snap.RepoRoot = root
	sg, err := safety.NewAt(root, cwd)
	if err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return 1, "", 0, true
	}
	sg.SetExtraDeny(r.Profile.Safety.ExtraReadDeny, r.Profile.Safety.ExtraCommandDeny)
	home, _ := os.UserHomeDir()
	extras := safety.NamedRoots(req.Text, home)
	if len(extras) > 0 {
		sg.AllowRoots(extras)
	}
	lister := func(ctx context.Context) ([]string, error) {
		return repo.Files(ctx, root)
	}
	toolOut := r.out()
	if !req.Plain {
		toolOut = ui.NewGutter(r.out())
	}
	onWeb := func(url, title, text string) {
		if r.Ingest != nil {
			r.Ingest.EnqueueWeb(projectID, url, title, text)
		}
	}
	onRecall := func(ctx context.Context, query, scope string, k int) ([]rag.Hit, error) {
		if r.Embed == nil || mem.Store() == nil {
			return nil, nil
		}
		return rag.SearchTune(ctx, mem.Store(), r.Embed, r.Embed, r.Profile.Name, projectID, query, scope, k, rag.TuneFrom(r.Profile))
	}
	for attempt := 0; attempt < 2; attempt++ {
		reg := tools.NewRegistry(sg, toolOut, lister, memory.NewWebCache(mem), memory.NewFactStore(mem, r.Profile.Name, projectID, sessionID), tools.Options{
			ReadOnlyShell:       dec.Intent == intent.Lookup,
			DocsTask:            dec.Task == intent.TaskDocs,
			Request:             req.Text,
			OnWebFetch:          onWeb,
			Recall:              onRecall,
			DisplayLines:        r.Profile.DisplayLines(),
			ShellDefaultTimeout: r.Profile.ShellDefaultTimeout(),
			ShellMaxTimeout:     r.Profile.ShellMaxTimeout(),
			ShellWaitDelay:      r.Profile.ShellWaitDelay(),
			DisableWeb:          !r.Profile.WebOn(),
		})
		reg = registryForRAG(reg, r.Profile.RAGEnabled())
		cfg := agent.Config{
			Client:         client,
			Tools:          reg,
			Confirmer:      confirmer,
			Out:            r.out(),
			Recorder:       rec,
			History:        history,
			AutoApprove:    req.Yes || r.Profile.Agent.Approve == "all_safe",
			Approve:        r.Profile.Agent.Approve,
			Prompt:         agent.ActPrompt + "\n" + lang,
			ThinkingPolicy: policyFor(*dec, r.Profile, thinkMode),
			Trace:          req.Trace,
			SessionID:      sessionID,
			Intent:         string(dec.Intent),
			RouterTime:     routerTime,
			Err:            r.errw(),
			Plain:          req.Plain,
			HideStats:      !r.Profile.ShowStatsLine(),
			Task:           string(dec.Task),
			Stats:          &agent.Stats{},
			OnShell:        r.RememberShell,
			Guard:          sg,
		}
		if files, cmds, text, ok := agent.RestoreApprovedPlan(history); ok && strings.Contains(req.Text, continueNote) {
			cfg.ResumePlan = true
			cfg.PlanFiles = files
			cfg.PlanCmds = cmds
			cfg.PlanText = text
		}
		sys := snap.String() + facts
		if len(extras) > 0 {
			sys += "\nAlso allowed this turn (named by the user): " + safety.DisplayRoots(extras, home)
		}
		sub := ""
		if inSubproject(cwd, root) {
			sub = "\n" + agent.SubprojectNote
		}
		switch dec.Intent {
		case intent.Lookup:
			cfg.Tools = reg.Named(lookupToolNames(r.Profile.RAGEnabled())...)
			cfg.Prompt = agent.LookupPrompt + "\n" + lang + sub
			cfg.MaxSteps = r.Profile.MaxStepsFor("lookup")
			cfg.ExploreBudget = r.Profile.ExploreBudgetFor("lookup", string(dec.Task))
			cfg.ExploreNote = agent.ExploreNoteLookup
		case intent.Build:
			cfg.Prompt = agent.CodePrompt + "\n" + lang + "\n" + agent.ScopeRule + sub
			cfg.MaxSteps = r.Profile.MaxStepsFor("build")
			cfg.RequirePlan = r.Profile.NeedPlan("build", false)
			cfg.ExploreBudget = r.Profile.ExploreBudgetFor("build", string(dec.Task))
			cfg.ExploreNote = agent.ExploreNoteBuild
			sys = codeContext(ctx, snap, root) + facts
		default:
			cfg.Prompt = agent.ActPrompt + "\n" + lang + "\n" + agent.ScopeRule + sub
			cfg.RequirePlan = r.Profile.NeedPlan("act", dec.EffectiveEffort() == intent.EffortHigh)
			cfg.MaxSteps = r.Profile.MaxStepsFor("act")
			cfg.ExploreBudget = r.Profile.ExploreBudgetFor("act", string(dec.Task))
			cfg.ExploreNote = agent.ExploreNoteLookup
		}
		cfg.Temperature = r.Profile.TemperatureFor(string(dec.Intent))
		cfg.TopP = r.Profile.LLM.TopP
		cfg.TokenCap = r.Profile.TokenCap(string(dec.Intent))
		cfg.ElideTrigger = r.Profile.ElideTrigger()
		cfg.ElideTarget = r.Profile.ElideTarget()
		cfg.IdenticalRejects = r.Profile.IdenticalRejectLimit()

		if dec.Intent == intent.Lookup && !r.escalated {
			r.emitBadge(req, "lookup", false)
		}

		err = agent.Run(ctx, cfg, sys, req.Text)
		if dec.Intent == intent.Lookup && errors.Is(err, tools.ErrLookupMutating) && !r.escalated && attempt == 0 {
			r.escalated = true
			dec.Intent = intent.Act
			r.emitBadge(req, "act", true)
			continue
		}
		if dec.Intent == intent.Build {
			printDiffStat(r.out(), root)
		}
		tok := 0
		if cfg.Stats != nil {
			tok = cfg.Stats.LastPromptTokens
		}
		if err == nil {
			return 0, "", tok, true
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return 130, "", tok, true
		}
		if errors.Is(err, agent.ErrAborted) {
			fmt.Fprintln(r.errw(), "konsolai: aborted")
			return 3, "", tok, true
		}
		if errors.Is(err, agent.ErrMaxSteps) {
			fmt.Fprintf(r.errw(), "konsolai: stopped after %d steps without finishing\n", cfg.MaxSteps)
			return 4, "", tok, true
		}
		if errors.Is(err, agent.ErrNoAnswer) {
			fmt.Fprintln(r.errw(), "konsolai: the model returned no answer")
			return 1, "", tok, true
		}
		if errors.Is(err, agent.ErrContextOverflow) {
			fmt.Fprintln(r.errw(), err.Error())
			return 1, "", tok, true
		}
		fmt.Fprintln(r.errw(), ModelErrorLine(r.Profile.MainEndpoint(), err, req.Trace))
		return 1, "", tok, true
	}
	return 1, "", 0, true
}

const fastWindowFillPct = 60

func FastWindowTooFull(est, window int) bool {
	return FastWindowTooFullRatio(est, window, 0)
}

func FastWindowTooFullRatio(est, window int, ratio float64) bool {
	if window <= 0 {
		window = 8192
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = float64(fastWindowFillPct) / 100
	}
	return float64(est) > float64(window)*ratio
}

func (r *Runner) fastPromptTooLarge(ctx context.Context, fc *llm.Client, snap sysctx.Snapshot, history []llm.Message, query, extra string) bool {
	_, _ = fc.ResolveModel(ctx)
	sys := llm.Message{Role: llm.RoleSystem, Content: extra + "\n\n" + snap.Brief()}
	user := llm.Message{Role: llm.RoleUser, Content: query}
	msgs := []llm.Message{sys}
	msgs = append(msgs, SanitizeAnswerHistory(history)...)
	msgs = append(msgs, user)
	return FastWindowTooFullRatio(fc.EstimatePromptTokens(msgs, nil), fc.ContextWindow(), r.Profile.FastGuardRatio())
}

func (r *Runner) runAnswer(ctx context.Context, main, client *llm.Client, snap sysctx.Snapshot, history []llm.Message, rec agent.Recorder, query, extra string, think bool, req Request, dec intent.Decision, routerTime time.Duration) (int, string, int, bool, intent.Intent) {
	if rec == nil {
		rec = agent.NopRecorder{}
	}
	system := llm.Message{Role: llm.RoleSystem, Content: extra + "\n\n" + snap.Brief()}
	user := llm.Message{Role: llm.RoleUser, Content: query}
	msgs := []llm.Message{system}
	msgs = append(msgs, SanitizeAnswerHistory(history)...)
	msgs = append(msgs, user)
	_ = rec.Message(ctx, user)

	usedMain := client == main
	t0 := time.Now()
	gate := &answerGate{dst: r.out(), onRelease: func() {
		r.emitBadge(req, string(dec.Intent), false)
	}}
	tokCap := r.Profile.AnswerEffortCap(string(dec.EffectiveEffort()))
	answerTools := answerEscalateTools(client, main)
	out, calls, usage, streamErr := streamAnswer(ctx, client, msgs, think, req.Plain, gate, r.errw(), r.Profile.TemperatureFor("answer"), r.Profile.LLM.TopP, tokCap, answerTools)
	cut := errors.Is(streamErr, llm.ErrTruncated)
	if streamErr != nil && !cut && !usedMain && !gate.released {
		if req.Trace {
			fmt.Fprintln(r.errw(), "fast model failed; retrying on main")
		}
		client = main
		usedMain = true
		gate.reset()
		answerTools = answerEscalateTools(client, main)
		out, calls, usage, streamErr = streamAnswer(ctx, client, msgs, think, req.Plain, gate, r.errw(), r.Profile.TemperatureFor("answer"), r.Profile.LLM.TopP, tokCap, answerTools)
		cut = errors.Is(streamErr, llm.ErrTruncated)
	}
	if streamErr == nil && strings.TrimSpace(out) == "" && len(calls) == 0 && !gate.released {
		gate.reset()
		out, calls, usage, streamErr = streamAnswer(ctx, client, []llm.Message{system, user}, think, req.Plain, gate, r.errw(), r.Profile.TemperatureFor("answer"), r.Profile.LLM.TopP, tokCap, answerTools)
		cut = errors.Is(streamErr, llm.ErrTruncated)
	}
	modelTime := time.Since(t0)
	ptok := 0
	if usedMain {
		ptok = usage.PromptTokens
	}
	if streamErr != nil && !cut {
		r.answerCut = false
		if errors.Is(streamErr, context.Canceled) {
			return 130, out, ptok, usedMain, ""
		}
		if errors.Is(streamErr, agent.ErrContextOverflow) {
			fmt.Fprintln(r.errw(), streamErr.Error())
			return 1, out, ptok, usedMain, ""
		}
		fmt.Fprintln(r.errw(), ModelErrorLine(r.Profile.MainEndpoint(), streamErr, req.Trace))
		return 1, out, ptok, usedMain, ""
	}
	if esc := parseEscalate(calls); esc != "" && !r.escalated {
		gate.discard()
		return 0, out, ptok, usedMain, esc
	}
	if strings.TrimSpace(out) == "" && !cut {
		r.answerCut = false
		fmt.Fprintln(r.errw(), "konsolai: the model returned no answer")
		return 1, out, ptok, usedMain, ""
	}
	if !r.escalated && intent.IsPromise(out) && !gate.released {
		gate.discard()
		return 0, out, ptok, usedMain, intent.EscalateIntent(query, r.Profile.Router.ActionVerbs, r.Profile.Router.Targets)
	}
	if !gate.released {
		gate.release()
	}
	if cut {
		fmt.Fprintln(r.out(), ui.Current().Muted(fmt.Sprintf("· cut at %d tokens; ask 'continue' for more", usage.CompletionTokens)))
		r.answerCut = true
	} else {
		r.answerCut = false
	}
	if strings.TrimSpace(out) != "" {
		_ = rec.Message(ctx, llm.Message{Role: llm.RoleAssistant, Content: out})
	}
	st := agent.Stats{
		Steps:            1,
		ModelTime:        modelTime,
		PromptTokens:     usage.PromptTokens,
		LastPromptTokens: usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		RouterTime:       routerTime,
	}
	if req.Trace {
		agent.PrintTrace(r.errw(), 1, think, modelTime, usage.PromptTokens, usage.CompletionTokens, main.ContextWindow(), nil)
	}
	if r.Profile.ShowStatsLine() {
		agent.PrintSummary(r.errw(), string(dec.Intent), st)
	}
	return 0, out, ptok, usedMain, ""
}

func SanitizeAnswerHistory(hist []llm.Message) []llm.Message {
	var keep []llm.Message
	for _, m := range hist {
		switch m.Role {
		case llm.RoleTool:
			continue
		case llm.RoleAssistant:
			if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) > 0 {
				continue
			}
			keep = append(keep, m)
		case llm.RoleUser:
			keep = append(keep, m)
		}
	}
	if len(keep) > 12 {
		keep = keep[len(keep)-12:]
	}
	return keep
}

func StreamAnswer(ctx context.Context, client *llm.Client, msgs []llm.Message, think, plain bool, outw, errw io.Writer, temp, topP float64) (string, llm.Usage, error) {
	out, _, usage, err := streamAnswer(ctx, client, msgs, think, plain, outw, errw, temp, topP, 0, nil)
	return out, usage, err
}

func answerEscalateTools(client, main *llm.Client) []llm.ToolSpec {
	if client == nil || main == nil || client != main {
		return nil
	}
	return []llm.ToolSpec{EscalateTool}
}

func streamAnswer(ctx context.Context, client *llm.Client, msgs []llm.Message, think, plain bool, outw, errw io.Writer, temp, topP float64, tokenCap int, tools []llm.ToolSpec) (string, []llm.ToolCall, llm.Usage, error) {
	if outw == nil {
		outw = os.Stdout
	}
	if errw == nil {
		errw = os.Stderr
	}
	var filter llm.ThinkFilter
	var collected strings.Builder
	live := ui.StartLive("thinking")
	first := true
	var consider streamConsiderer
	if c, ok := outw.(streamConsiderer); ok {
		consider = c
	}
	body := outw
	if !plain {
		body = ui.NewIndentWriter(outw, 4)
	}
	rend := ui.NewRenderer(body, plain)
	if _, err := client.ResolveModel(ctx); err != nil {
		live.Stop()
		return "", nil, llm.Usage{}, err
	}
	_, maxTok, elide, err := agent.PrepareCall(client, msgs, tools, "answer")
	if err != nil {
		live.Stop()
		return "", nil, llm.Usage{}, err
	}
	if tokenCap > 0 && maxTok > tokenCap {
		maxTok = tokenCap
	}
	if elide.Count > 0 {
		fmt.Fprintln(errw, agent.FormatElide(elide, client.ContextWindow()))
	}
	if temp <= 0 {
		temp = 0.3
	}
	msg, usage, err := client.StreamTools(ctx, msgs, tools, llm.ChatOptions{
		Thinking: think, Temperature: temp, TopP: topP, MaxTokens: maxTok,
		OnProgress: func(p llm.Progress) { live.SetLabel(agent.ProgressLabel("thinking", p)) },
	}, func(delta string) error {
		s := filter.Write(delta)
		if first && s != "" {
			live.Stop()
			ui.BeforeOutput()
			first = false
		}
		collected.WriteString(s)
		_, werr := rend.Write([]byte(s))
		if consider != nil {
			consider.Consider(collected.String())
		}
		return werr
	})
	if first {
		live.Stop()
	}
	rest := filter.Flush()
	collected.WriteString(rest)
	if rest != "" {
		_, _ = rend.Write([]byte(rest))
		if consider != nil {
			consider.Consider(collected.String())
		}
	}
	_ = rend.Flush()
	if consider != nil {
		consider.Consider(collected.String())
	}
	out := strings.TrimLeft(collected.String(), "\r\n")
	if out == "" {
		out = strings.TrimLeft(msg.Content, "\r\n")
	}
	return out, msg.ToolCalls, usage, err
}

func codeContext(ctx context.Context, snap sysctx.Snapshot, root string) string {
	var b strings.Builder
	b.WriteString(snap.String())
	b.WriteString("\n\nrepo root: ")
	b.WriteString(root)
	proj := repo.Detect(root)
	if len(proj.Kinds) > 0 {
		b.WriteString("\nproject kinds: ")
		b.WriteString(strings.Join(proj.Kinds, ", "))
	}
	if len(proj.Commands) > 0 {
		b.WriteString("\nproject commands:\n")
		for _, c := range proj.Commands {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteByte('\n')
		}
	}
	files, err := repo.Files(ctx, root)
	if err == nil {
		if m := repo.Map(files, 120); m != "" {
			b.WriteString("\nrepo map:\n")
			b.WriteString(m)
			b.WriteByte('\n')
		}
	}
	instr, err := repo.Instructions(root)
	if err == nil && instr != "" {
		b.WriteString("\nproject instructions:\n")
		b.WriteString(instr)
		if !strings.HasSuffix(instr, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (r *Runner) attachSessionMemory(ctx context.Context, mem *memory.Guard, projectID *int64, text string, needed bool) (int, string) {
	if !needed || r.Embed == nil || mem == nil || mem.Store() == nil {
		return 0, text
	}
	st := mem.Store()
	if !st.RAGAvailable(ctx) {
		return 0, text
	}
	hits, err := rag.SearchTune(ctx, st, r.Embed, r.Embed, r.Profile.Name, projectID, text, "sessions", 4, rag.TuneFrom(r.Profile))
	if err != nil || len(hits) == 0 {
		return 0, text
	}
	return len(hits), rag.PrependMemory(text, hits)
}

func AttachMemories(user string, hits []rag.Hit) (string, int) {
	if len(hits) == 0 {
		return user, 0
	}
	return rag.PrependMemory(user, hits), len(hits)
}

func registryForRAG(reg *tools.Registry, rag bool) *tools.Registry {
	if rag {
		return reg
	}
	return reg.Without("recall")
}

func lookupToolNames(rag bool) []string {
	names := []string{"list_dir", "read_file", "outline", "search", "find_files", "web_fetch"}
	if rag {
		names = append(names, "recall")
	}
	return append(names, "run_shell", "finish")
}

func inSubproject(cwd, root string) bool {
	c, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		c = filepath.Clean(cwd)
	}
	rt, err := filepath.EvalSymlinks(root)
	if err != nil {
		rt = filepath.Clean(root)
	}
	return c != rt
}

// streamConsiderer releases held answer text once it is safe to show.
type streamConsiderer interface {
	Consider(text string)
}

// answerGate holds rendered answer bytes until the first complete line is
// not a promise, then copies them to dst. A pure promise stays unreleased
// so the caller can discard it and escalate.
type answerGate struct {
	dst       io.Writer
	hold      bytes.Buffer
	released  bool
	suppress  bool
	onRelease func()
}

func (g *answerGate) Write(p []byte) (int, error) {
	if g == nil {
		return len(p), nil
	}
	if g.released {
		if g.dst == nil {
			return len(p), nil
		}
		return g.dst.Write(p)
	}
	return g.hold.Write(p)
}

func (g *answerGate) Consider(text string) {
	if g == nil || g.released || g.suppress {
		return
	}
	if strings.TrimSpace(text) == "" || !strings.Contains(text, "\n") {
		return
	}
	if intent.IsPromise(text) {
		return
	}
	g.release()
}

func (g *answerGate) release() {
	if g == nil || g.released || g.suppress {
		return
	}
	g.released = true
	if g.onRelease != nil {
		fn := g.onRelease
		g.onRelease = nil
		fn()
	}
	if g.hold.Len() == 0 || g.dst == nil {
		g.hold.Reset()
		return
	}
	_, _ = io.Copy(g.dst, &g.hold)
	g.hold.Reset()
}

func (g *answerGate) discard() {
	if g == nil {
		return
	}
	g.suppress = true
	g.hold.Reset()
}

func (g *answerGate) reset() {
	if g == nil {
		return
	}
	g.released = false
	g.suppress = false
	g.hold.Reset()
}

func printDiffStat(w io.Writer, root string) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return
	}
	out, err := exec.Command("git", "-C", root, "diff", "--stat").Output()
	if err != nil {
		return
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return
	}
	fmt.Fprintln(w, ui.Dim(s))
}
