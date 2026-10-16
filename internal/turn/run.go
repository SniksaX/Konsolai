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
	facts := LoadFactsBlock(ctx, mem, r.Profile.Name, projectID)
	conf, closeConf, err := r.confirm(req.Yes)
	if err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return Result{ExitCode: 1}
	}
	defer closeConf()

	cands := FindCandidates(ctx, mem, r.Profile, projectID, cwd)
	routerCands := ToIntentCandidates(cands)
	if req.New {
		routerCands = nil
	}
	root := repo.Root(cwd)
	inRepo := false
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		inRepo = true
	}
	routedAt := time.Now()
	understand := ui.Start("understanding")
	dec := DecideIntent(ctx, client, req.Intent, req.Text, req.New, routerCands, inRepo)
	understand()
	routerTime := time.Since(routedAt)

	var picked ResumeCandidate
	if req.ResumeSession != "" {
		picked = loadSessionByID(ctx, mem, req.ResumeSession, cands)
	} else {
		picked = ApplyResume(req.New, dec, cands)
	}
	if picked.ID != "" && len(picked.Messages) == 0 {
		if s := mem.Store(); s != nil {
			msgs, err := s.LoadMessages(ctx, picked.ID)
			if err != nil {
				mem.Fail(err)
				picked = ResumeCandidate{}
			} else {
				picked.Messages = memory.ToLLM(msgs)
			}
		}
	}
	sessionID, history := picked.ID, picked.Messages
	if len(history) > 0 {
		history = agent.LeanResume(history, client.TokensPerChar())
		fmt.Fprintln(r.out(), ui.Dim("↻ continuing session: "+picked.Title))
	}
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
	code, answer := r.runProfile(ctx, client, snap, facts, history, tap, mem, sessionID, projectID, req, cwd, conf, dec, routerTime)
	if tap.answer != "" {
		answer = tap.answer
	}
	if sessionID != "" {
		if s := mem.Store(); s != nil {
			endCtx, cancel := memory.Isolated(ctx)
			defer cancel()
			if err := s.EndSession(endCtx, sessionID, SessionStatus(code)); err != nil {
				mem.Fail(err)
			}
		}
	}
	if mem.Store() == nil {
		title := req.Text
		if utf8.RuneCountInString(title) > 80 {
			title = string([]rune(title)[:80])
		}
		if !req.New {
			if prev, ok := memory.LoadLast(r.Profile.Name, cwd, time.Duration(r.Profile.SessionIdleMinutes)*time.Minute); ok && prev.Title != "" {
				title = prev.Title
			}
		}
		_ = memory.SaveLast(memory.LastSession{
			Profile:  r.Profile.Name,
			CWD:      cwd,
			Title:    title,
			Messages: tap.msgs,
		})
	}
	return Result{SessionID: sessionID, Intent: string(dec.Intent), ExitCode: code, Answer: answer}
}

func loadSessionByID(ctx context.Context, mem *memory.Guard, id string, cands []ResumeCandidate) ResumeCandidate {
	for _, c := range cands {
		if c.ID == id {
			return c
		}
	}
	if s := mem.Store(); s != nil {
		msgs, err := s.LoadMessages(ctx, id)
		if err != nil {
			mem.Fail(err)
			return ResumeCandidate{}
		}
		return ResumeCandidate{ID: id, Messages: memory.ToLLM(msgs)}
	}
	return ResumeCandidate{}
}

func langLine(d intent.Decision) string {
	lang := d.Language
	if lang == "" {
		lang = "en"
	}
	return "Reply in the user's language (" + lang + ")."
}

func (r *Runner) runProfile(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, facts string, history []llm.Message, rec agent.Recorder, mem *memory.Guard, sessionID string, projectID *int64, req Request, cwd string, confirmer agent.Confirmer, dec intent.Decision, routerTime time.Duration) (int, string) {
	lang := langLine(dec)
	if dec.Intent == intent.Answer {
		code, ans := r.runAnswer(ctx, client, snap, history, rec, req.Text, agent.AnswerPrompt+"\n"+lang, dec.Reasoning, req, dec, routerTime)
		return code, ans
	}

	root := repo.Root(cwd)
	snap.RepoRoot = root
	sg, err := safety.NewAt(root, cwd)
	if err != nil {
		fmt.Fprintln(r.errw(), "konsolai:", err)
		return 1, ""
	}
	lister := func(ctx context.Context) ([]string, error) {
		return repo.Files(ctx, root)
	}
	toolOut := r.out()
	if !req.Plain {
		toolOut = ui.NewGutter(r.out())
	}
	reg := tools.NewRegistry(sg, toolOut, lister, memory.NewWebCache(mem), memory.NewFactStore(mem, r.Profile.Name, projectID, sessionID), tools.Options{
		ReadOnlyShell: dec.Intent == intent.Lookup,
		DocsTask:      dec.Task == intent.TaskDocs,
		Request:       req.Text,
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
		ThinkingPolicy: ThinkingFor(dec, r.Profile.BuildThinking),
		Trace:          req.Trace,
		SessionID:      sessionID,
		Intent:         string(dec.Intent),
		RouterTime:     routerTime,
		Err:            r.errw(),
		Plain:          req.Plain,
		Task:           string(dec.Task),
	}
	sys := snap.String() + facts
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
		cfg.RequirePlan = true
		cfg.MaxSteps = 15
		cfg.ExploreBudget = agent.ExploreBudgetFor("act", string(dec.Task))
		cfg.ExploreNote = agent.ExploreNoteLookup
	}

	err = agent.Run(ctx, cfg, sys, req.Text)
	if dec.Intent == intent.Build {
		printDiffStat(r.out(), root)
	}
	if err == nil {
		return 0, ""
	}
	if errors.Is(err, context.Canceled) {
		return 130, ""
	}
	if errors.Is(err, agent.ErrAborted) {
		fmt.Fprintln(r.errw(), "konsolai: aborted")
		return 3, ""
	}
	if errors.Is(err, agent.ErrMaxSteps) {
		fmt.Fprintf(r.errw(), "konsolai: stopped after %d steps without finishing\n", cfg.MaxSteps)
		return 4, ""
	}
	if errors.Is(err, agent.ErrNoAnswer) {
		fmt.Fprintln(r.errw(), "konsolai: the model returned no answer")
		return 1, ""
	}
	if errors.Is(err, agent.ErrContextOverflow) {
		fmt.Fprintln(r.errw(), err.Error())
		return 1, ""
	}
	fmt.Fprintln(r.errw(), ModelErrorLine(r.Profile.Endpoint, err, req.Trace))
	return 1, ""
}

func (r *Runner) runAnswer(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, history []llm.Message, rec agent.Recorder, query, extra string, think bool, req Request, dec intent.Decision, routerTime time.Duration) (int, string) {
	if rec == nil {
		rec = agent.NopRecorder{}
	}
	system := llm.Message{Role: llm.RoleSystem, Content: extra + "\n\n" + snap.Brief()}
	user := llm.Message{Role: llm.RoleUser, Content: query}
	msgs := []llm.Message{system}
	msgs = append(msgs, SanitizeAnswerHistory(history)...)
	msgs = append(msgs, user)
	_ = rec.Message(ctx, user)

	t0 := time.Now()
	out, usage, streamErr := StreamAnswer(ctx, client, msgs, think, req.Plain, r.out(), r.errw())
	if streamErr == nil && strings.TrimSpace(out) == "" {
		out, usage, streamErr = StreamAnswer(ctx, client, []llm.Message{system, user}, think, req.Plain, r.out(), r.errw())
	}
	modelTime := time.Since(t0)
	_ = rec.Message(ctx, llm.Message{Role: llm.RoleAssistant, Content: out})
	st := agent.Stats{
		Steps:            1,
		ModelTime:        modelTime,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		RouterTime:       routerTime,
	}
	if req.Trace {
		agent.PrintTrace(r.errw(), 1, think, modelTime, usage.PromptTokens, usage.CompletionTokens, client.ContextWindow(), nil)
	}
	agent.PrintSummary(r.errw(), string(dec.Intent), st)

	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			return 130, out
		}
		if errors.Is(streamErr, agent.ErrContextOverflow) {
			fmt.Fprintln(r.errw(), streamErr.Error())
			return 1, out
		}
		fmt.Fprintln(r.errw(), ModelErrorLine(r.Profile.Endpoint, streamErr, req.Trace))
		return 1, out
	}
	if strings.TrimSpace(out) == "" {
		fmt.Fprintln(r.errw(), "konsolai: the model returned no answer")
		return 1, out
	}
	return 0, out
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
