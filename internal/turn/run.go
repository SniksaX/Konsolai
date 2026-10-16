package turn

import (
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

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/sysctx"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
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
	client := r.client()
	if _, err := client.ResolveModel(ctx); err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return Result{ExitCode: 1}
	}
	thinkMode := r.ThinkMode
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
	if !continued {
		dec = DecideIntent(ctx, routeClient, req.Intent, req.Text, req.New, routerCands, inRepo)
	} else {
		req.Text = strings.TrimSpace(req.Text) + "\n\n" + continueNote
	}
	understand()
	routerTime := time.Since(routedAt)
	if thinkMode == ThinkAlways {
		dec.Effort = intent.EffortHigh
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
	win := client.ContextWindow()
	r.LastWindow = win
	if ShouldCompact(r.LastPromptTokens, win) && len(history) > compactKeep {
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
	fmt.Fprintln(r.out())
	if req.Plain {
		fmt.Fprintln(r.out(), "· "+string(dec.Intent))
	} else {
		fmt.Fprintln(r.out(), ui.IntentBadge(string(dec.Intent)))
	}
	if sessionID == "" && len(history) == 0 {
		sessionID = CreateSession(ctx, mem, r.Profile, projectID, cwd, string(dec.Intent), req.Text)
	}
	rec := memory.NewRecorder(mem, sessionID, cwd)
	tap := &sessionTap{inner: rec, msgs: append([]llm.Message(nil), history...)}
	if len(tap.msgs) > 20 {
		tap.msgs = tap.msgs[len(tap.msgs)-20:]
	}
	code, answer, ptok, usedMain := r.runProfile(ctx, client, snap, facts, history, tap, mem, sessionID, projectID, req, cwd, conf, dec, routerTime, thinkMode)
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
	if sessionID != "" && !req.KeepOpen {
		if s := mem.Store(); s != nil {
			endCtx, cancel := memory.Isolated(ctx)
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

func (r *Runner) runProfile(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, facts string, history []llm.Message, rec agent.Recorder, mem *memory.Guard, sessionID string, projectID *int64, req Request, cwd string, confirmer agent.Confirmer, dec intent.Decision, routerTime time.Duration, thinkMode ThinkMode) (int, string, int, bool) {
	dec.Language = intent.ResolveLanguage(req.Text, lastUserOf(history), dec.Language, r.Profile.DefaultLanguage, r.Profile.Languages)
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
		think := dec.EffectiveEffort() == intent.EffortHigh || thinkMode == ThinkAlways
		if thinkMode == ThinkNever {
			think = false
		}
		return r.runAnswer(ctx, client, answerClient, snap, history, rec, req.Text, prompt, think, req, dec, routerTime)
	}

	root := repo.Root(cwd)
	snap.RepoRoot = root
	sg, err := safety.NewAt(root, cwd)
	if err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return 1, "", 0, true
	}
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
	reg := tools.NewRegistry(sg, toolOut, lister, memory.NewWebCache(mem), memory.NewFactStore(mem, r.Profile.Name, projectID, sessionID), tools.Options{
		ReadOnlyShell: dec.Intent == intent.Lookup,
		DocsTask:      dec.Task == intent.TaskDocs,
		Request:       req.Text,
		OnWebFetch:    onWeb,
	})
	cfg := agent.Config{
		Client:         client,
		Tools:          reg,
		Confirmer:      confirmer,
		Out:            r.out(),
		Recorder:       rec,
		History:        history,
		AutoApprove:    req.Yes,
		Prompt:         agent.ActPrompt + "\n" + lang,
		ThinkingPolicy: ThinkingFor(dec, r.Profile.BuildThinking, thinkMode),
		Trace:          req.Trace,
		SessionID:      sessionID,
		Intent:         string(dec.Intent),
		RouterTime:     routerTime,
		Err:            r.errw(),
		Plain:          req.Plain,
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
		cfg.Tools = reg.Named("list_dir", "read_file", "outline", "search", "find_files", "web_fetch", "run_shell", "finish")
		cfg.Prompt = agent.LookupPrompt + "\n" + lang + sub
		cfg.MaxSteps = 10
		cfg.ExploreBudget = agent.ExploreBudgetFor("lookup", string(dec.Task))
		cfg.ExploreNote = agent.ExploreNoteLookup
	case intent.Build:
		cfg.Prompt = agent.CodePrompt + "\n" + lang + "\n" + agent.ScopeRule + sub
		cfg.Temperature = 0.3
		cfg.MaxSteps = 60
		cfg.RequirePlan = true
		cfg.ExploreBudget = agent.ExploreBudgetFor("build", string(dec.Task))
		cfg.ExploreNote = agent.ExploreNoteBuild
		sys = codeContext(ctx, snap, root) + facts
	default:
		cfg.Prompt = agent.ActPrompt + "\n" + lang + "\n" + agent.ScopeRule + sub
		if dec.EffectiveEffort() == intent.EffortHigh {
			cfg.RequirePlan = true
		}
		cfg.MaxSteps = 15
		cfg.ExploreBudget = agent.ExploreBudgetFor("act", string(dec.Task))
		cfg.ExploreNote = agent.ExploreNoteLookup
	}

	err = agent.Run(ctx, cfg, sys, req.Text)
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
	fmt.Fprintln(r.errw(), ModelErrorLine(r.Profile.Endpoint, err, req.Trace))
	return 1, "", tok, true
}

const fastWindowFillPct = 60

func FastWindowTooFull(est, window int) bool {
	if window <= 0 {
		window = 8192
	}
	return est > window*fastWindowFillPct/100
}

func (r *Runner) fastPromptTooLarge(ctx context.Context, fc *llm.Client, snap sysctx.Snapshot, history []llm.Message, query, extra string) bool {
	_, _ = fc.ResolveModel(ctx)
	sys := llm.Message{Role: llm.RoleSystem, Content: extra + "\n\n" + snap.Brief()}
	user := llm.Message{Role: llm.RoleUser, Content: query}
	msgs := []llm.Message{sys}
	msgs = append(msgs, SanitizeAnswerHistory(history)...)
	msgs = append(msgs, user)
	return FastWindowTooFull(fc.EstimatePromptTokens(msgs, nil), fc.ContextWindow())
}

func (r *Runner) runAnswer(ctx context.Context, main, client *llm.Client, snap sysctx.Snapshot, history []llm.Message, rec agent.Recorder, query, extra string, think bool, req Request, dec intent.Decision, routerTime time.Duration) (int, string, int, bool) {
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
	out, usage, streamErr := StreamAnswer(ctx, client, msgs, think, req.Plain, r.out(), r.errw())
	if streamErr != nil && !usedMain {
		if req.Trace {
			fmt.Fprintln(r.errw(), "fast model failed; retrying on main")
		}
		client = main
		usedMain = true
		out, usage, streamErr = StreamAnswer(ctx, client, msgs, think, req.Plain, r.out(), r.errw())
	}
	if streamErr == nil && strings.TrimSpace(out) == "" {
		out, usage, streamErr = StreamAnswer(ctx, client, []llm.Message{system, user}, think, req.Plain, r.out(), r.errw())
	}
	modelTime := time.Since(t0)
	_ = rec.Message(ctx, llm.Message{Role: llm.RoleAssistant, Content: out})
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
	agent.PrintSummary(r.errw(), string(dec.Intent), st)

	ptok := 0
	if usedMain {
		ptok = usage.PromptTokens
	}
	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			return 130, out, ptok, usedMain
		}
		if errors.Is(streamErr, agent.ErrContextOverflow) {
			fmt.Fprintln(r.errw(), streamErr.Error())
			return 1, out, ptok, usedMain
		}
		fmt.Fprintln(r.errw(), ModelErrorLine(r.Profile.Endpoint, streamErr, req.Trace))
		return 1, out, ptok, usedMain
	}
	if strings.TrimSpace(out) == "" {
		fmt.Fprintln(r.errw(), "konsolai: the model returned no answer")
		return 1, out, ptok, usedMain
	}
	return 0, out, ptok, usedMain
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

func StreamAnswer(ctx context.Context, client *llm.Client, msgs []llm.Message, think, plain bool, outw, errw io.Writer) (string, llm.Usage, error) {
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
	rend := ui.NewRenderer(outw, plain)
	if _, err := client.ResolveModel(ctx); err != nil {
		return "", llm.Usage{}, err
	}
	_, maxTok, elide, err := agent.PrepareCall(client, msgs, nil, "answer")
	if err != nil {
		return "", llm.Usage{}, err
	}
	if elide.Count > 0 {
		fmt.Fprintln(errw, agent.FormatElide(elide, client.ContextWindow()))
	}
	usage, err := client.Stream(ctx, msgs, llm.ChatOptions{
		Thinking: think, Temperature: 0.3, MaxTokens: maxTok,
		OnProgress: func(p llm.Progress) { live.SetLabel(agent.ProgressLabel("thinking", p)) },
	}, func(delta string) error {
		s := filter.Write(delta)
		if first && s != "" {
			live.Stop()
			ui.BeforeOutput()
			first = false
			_, _ = io.WriteString(outw, "\n")
		}
		collected.WriteString(s)
		_, werr := rend.Write([]byte(s))
		return werr
	})
	if first {
		live.Stop()
	}
	rest := filter.Flush()
	collected.WriteString(rest)
	if rest != "" {
		_, _ = rend.Write([]byte(rest))
	}
	_ = rend.Flush()
	out := strings.TrimLeft(collected.String(), "\r\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		_, _ = io.WriteString(outw, "\n")
	}
	return out, usage, err
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
