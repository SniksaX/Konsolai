package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/sysctx"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

var _ agent.Confirmer = (*ui.Prompter)(nil)

const (
	usageText = `usage: konsolai [--yes] [--profile NAME] [--intent answer|lookup|act|build] [--trace] [--plain] [new] ["<request>"]`

	noMemory = "memory is not configured (see ~/.config/konsolai/config.json)"
)

type command int

const (
	cmdRequest command = iota
	cmdHistory
	cmdUndo
	cmdFacts
	cmdPrune
	cmdRemember
	cmdForget
)

type invocation struct {
	Intent    string
	Request   string
	Yes       bool
	Profile   string
	New       bool
	Trace     bool
	Plain     bool
	Cmd       command
	FactScope string
	FactID    int64
	Fact      string
	Endpoint  string
}

var errRequest = errors.New(`request must be a single argument in double quotes, e.g. konsolai code "add a /health endpoint"`)

func parseArgs(args []string) (invocation, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("konsolai", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "auto-approve non-denylisted actions")
	profile := fs.String("profile", "", "config profile")
	intent := fs.String("intent", "", "skip the router")
	trace := fs.Bool("trace", false, "print per-call timing and tokens")
	plain := fs.Bool("plain", false, "disable styled output")
	project := fs.Bool("project", false, "project-scoped fact")
	if err := fs.Parse(args); err != nil {
		return invocation{}, err
	}
	if *intent != "" && *intent != "answer" && *intent != "lookup" && *intent != "act" && *intent != "build" {
		return invocation{}, errRequest
	}
	rest := fs.Args()
	inv := invocation{Yes: *yes, Profile: *profile, Intent: *intent, Trace: *trace, Plain: *plain}
	if len(rest) == 0 {
		return inv, nil
	}
	switch rest[0] {
	case "history":
		if len(rest) != 1 {
			return invocation{}, errRequest
		}
		inv.Cmd = cmdHistory
		return inv, nil
	case "undo":
		if len(rest) != 1 {
			return invocation{}, errRequest
		}
		inv.Cmd = cmdUndo
		return inv, nil
	case "facts":
		if len(rest) != 1 {
			return invocation{}, errRequest
		}
		inv.Cmd = cmdFacts
		return inv, nil
	case "prune":
		if len(rest) != 1 {
			return invocation{}, errRequest
		}
		inv.Cmd = cmdPrune
		return inv, nil
	case "remember":
		rest = rest[1:]
		if len(rest) > 0 && rest[0] == "--project" {
			*project = true
			rest = rest[1:]
		}
		if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
			return invocation{}, errRequest
		}
		inv.Cmd = cmdRemember
		inv.Fact = rest[0]
		inv.FactScope = "user"
		if *project {
			inv.FactScope = "project"
		}
		return inv, nil
	case "forget":
		if len(rest) != 3 || (rest[1] != "user" && rest[1] != "project") {
			return invocation{}, errRequest
		}
		id, err := strconv.ParseInt(rest[2], 10, 64)
		if err != nil || id < 1 {
			return invocation{}, errRequest
		}
		inv.Cmd = cmdForget
		inv.FactScope = rest[1]
		inv.FactID = id
		return inv, nil
	}
	if rest[0] == "new" {
		inv.New = true
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return inv, nil
	}
	if len(rest) != 1 {
		return invocation{}, errRequest
	}
	inv.Request = rest[0]
	return inv, nil
}

func main() {
	os.Exit(run())
}

func run() int {
	inv, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, usageText)
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, err)
		}
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	file, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	prof, err := file.Resolve(inv.Profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	inv.Endpoint = prof.Endpoint

	needMem := inv.Cmd != cmdRequest
	var st *store.Store
	if prof.DatabaseURL == "" {
		if needMem {
			fmt.Fprintln(os.Stderr, noMemory)
			return 1
		}
	} else {
		clear := ui.StartAfter("loading memory", 300*time.Millisecond)
		opened, err := store.Open(ctx, prof.DatabaseURL)
		clear()
		if err != nil {
			if needMem {
				fmt.Fprintln(os.Stderr, "konsolai:", err)
				return 1
			}
			fmt.Fprintln(os.Stderr, ui.Dim(config.DisableLine(prof.DatabaseURL, err)))
		} else {
			st = opened
			defer st.Close()
		}
	}
	mem := memory.NewGuard(st, func(err error) {
		fmt.Fprintln(os.Stderr, ui.Dim(config.DisableLine(prof.DatabaseURL, err)))
	})
	if s := mem.Store(); s != nil {
		if err := s.PruneIfDue(ctx, prof.RetentionDays); err != nil {
			mem.Fail(err)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	projectID := upsertProject(ctx, mem, cwd)

	switch inv.Cmd {
	case cmdHistory:
		return runHistory(ctx, mem, prof.Name)
	case cmdUndo:
		return runUndo(ctx, mem, projectID, cwd, inv.Yes)
	case cmdFacts:
		return runFacts(ctx, mem, prof.Name, projectID)
	case cmdPrune:
		return runPrune(ctx, mem, prof.RetentionDays)
	case cmdRemember:
		return runRemember(ctx, mem, prof.Name, projectID, inv.Fact, inv.FactScope)
	case cmdForget:
		return runForget(ctx, mem, inv.FactScope, inv.FactID)
	}

	inv, err = resolveRequest(inv)
	if err != nil {
		fmt.Fprintln(os.Stderr, usageText)
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	snap, err := sysctx.Collect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	client := llm.New(prof.Endpoint, prof.Model)
	client.SetIdleTimeout(time.Duration(prof.LLMIdleTimeoutSeconds) * time.Second)
	facts := loadFactsBlock(ctx, mem, prof.Name, projectID)

	prompter, err := ui.OpenPrompter(inv.Yes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	cands := findCandidates(ctx, mem, prof, projectID, cwd)
	routerCands := toIntentCandidates(cands)
	if inv.New {
		routerCands = nil
	}
	root := repo.Root(cwd)
	inRepo := false
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		inRepo = true
	}
	routedAt := time.Now()
	understand := ui.Start("understanding")
	dec := decideIntent(ctx, client, inv, routerCands, inRepo)
	understand()
	routerTime := time.Since(routedAt)
	picked := applyResume(inv, dec, cands)
	if picked.ID != "" && len(picked.Messages) == 0 {
		if s := mem.Store(); s != nil {
			msgs, err := s.LoadMessages(ctx, picked.ID)
			if err != nil {
				mem.Fail(err)
				picked = resumeCandidate{}
			} else {
				picked.Messages = memory.ToLLM(msgs)
			}
		}
	}
	sessionID, history := picked.ID, picked.Messages
	if len(history) > 0 {
		history = agent.LeanResume(history, client.TokensPerChar())
		fmt.Fprintln(os.Stdout, ui.Dim("↻ continuing session: "+picked.Title))
	}
	if inv.Plain {
		fmt.Fprintln(os.Stdout, "· "+string(dec.Intent))
	} else {
		fmt.Fprintln(os.Stdout, ui.IntentBadge(string(dec.Intent)))
	}
	if sessionID == "" && len(history) == 0 {
		sessionID = createSession(ctx, mem, prof, projectID, cwd, string(dec.Intent), inv.Request)
	}
	rec := memory.NewRecorder(mem, sessionID, cwd)
	tap := &sessionTap{inner: rec, msgs: append([]llm.Message(nil), history...)}
	if len(tap.msgs) > 20 {
		tap.msgs = tap.msgs[len(tap.msgs)-20:]
	}
	code := runProfile(ctx, client, snap, facts, history, tap, mem, sessionID, projectID, prof, inv, cwd, prompter, dec, routerTime)
	if sessionID != "" {
		if s := mem.Store(); s != nil {
			endCtx, cancel := memory.Isolated(ctx)
			defer cancel()
			if err := s.EndSession(endCtx, sessionID, sessionStatus(code)); err != nil {
				mem.Fail(err)
			}
		}
	}
	if mem.Store() == nil {
		title := inv.Request
		if utf8.RuneCountInString(title) > 80 {
			title = string([]rune(title)[:80])
		}
		if !inv.New {
			if prev, ok := memory.LoadLast(prof.Name, cwd, time.Duration(prof.SessionIdleMinutes)*time.Minute); ok && prev.Title != "" {
				title = prev.Title
			}
		}
		_ = memory.SaveLast(memory.LastSession{
			Profile:  prof.Name,
			CWD:      cwd,
			Title:    title,
			Messages: tap.msgs,
		})
	}
	return code
}

func upsertProject(ctx context.Context, mem *memory.Guard, cwd string) *int64 {
	root := repo.Root(cwd)
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}
	s := mem.Store()
	if s == nil {
		return nil
	}
	p := repo.Detect(root)
	id, err := s.UpsertProject(ctx, root, p.Kinds, p.Commands)
	if err != nil {
		mem.Fail(err)
		return nil
	}
	return &id
}

type resumeCandidate struct {
	ID       string
	Title    string
	LastUser string
	Age      time.Duration
	Messages []llm.Message
}

func findCandidates(ctx context.Context, mem *memory.Guard, prof config.Profile, projectID *int64, cwd string) []resumeCandidate {
	idle := time.Duration(prof.SessionIdleMinutes) * time.Minute
	if mem.Store() == nil {
		if sess, ok := memory.LoadLast(prof.Name, cwd, idle); ok {
			age := time.Duration(0)
			if !sess.UpdatedAt.IsZero() {
				age = time.Since(sess.UpdatedAt)
			}
			return []resumeCandidate{{
				Title: sess.Title, LastUser: lastUserOf(sess.Messages), Age: age, Messages: sess.Messages,
			}}
		}
		return nil
	}
	rows, err := mem.Store().RecentSessions(ctx, prof.Name, projectID, cwd, idle, 3)
	if err != nil {
		mem.Fail(err)
		return nil
	}
	out := make([]resumeCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, resumeCandidate{ID: r.ID, Title: r.Title, LastUser: r.LastUserMessage, Age: r.Age})
	}
	return out
}

func lastUserOf(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			s := msgs[i].Content
			if utf8.RuneCountInString(s) > 120 {
				s = string([]rune(s)[:120])
			}
			return s
		}
	}
	return ""
}

func toIntentCandidates(cands []resumeCandidate) []intent.Candidate {
	out := make([]intent.Candidate, len(cands))
	for i, c := range cands {
		out[i] = intent.Candidate{Title: c.Title, LastUser: c.LastUser, Age: c.Age}
	}
	return out
}

func applyResume(inv invocation, dec intent.Decision, cands []resumeCandidate) resumeCandidate {
	if inv.New || dec.ContinuesSession <= 0 || dec.ContinuesSession > len(cands) {
		return resumeCandidate{}
	}
	return cands[dec.ContinuesSession-1]
}

func createSession(ctx context.Context, mem *memory.Guard, prof config.Profile, projectID *int64, cwd, mode, request string) string {
	s := mem.Store()
	if s == nil {
		return ""
	}
	title := request
	if utf8.RuneCountInString(title) > 80 {
		title = string([]rune(title)[:80])
	}
	id, err := s.CreateSession(ctx, store.NewSession{
		Profile: prof.Name, ProjectID: projectID, CWD: cwd,
		Mode: mode, Model: prof.Model, Title: title,
	})
	if err != nil {
		mem.Fail(err)
		return ""
	}
	return id
}

func loadFactsBlock(ctx context.Context, mem *memory.Guard, profile string, projectID *int64) string {
	s := mem.Store()
	if s == nil {
		return ""
	}
	user, proj, err := s.ListFacts(ctx, profile, projectID)
	if err != nil {
		mem.Fail(err)
		return ""
	}
	return formatFacts(user, proj)
}

func formatFacts(user, proj []store.Fact) string {
	var b strings.Builder
	write := func(title string, facts []store.Fact) {
		if len(facts) == 0 {
			return
		}
		if len(facts) > 40 {
			facts = facts[:40]
		}
		b.WriteString("\n\n")
		b.WriteString(title)
		b.WriteByte('\n')
		n := 0
		for _, f := range facts {
			line := fmt.Sprintf("- [%d] %s\n", f.ID, f.Fact)
			if n+len(line) > 2048 {
				break
			}
			b.WriteString(line)
			n += len(line)
		}
	}
	write("User facts:", user)
	write("Project facts:", proj)
	return b.String()
}

func sessionStatus(code int) string {
	switch code {
	case 0:
		return "finished"
	case 3, 130:
		return "aborted"
	default:
		return "failed"
	}
}

func decideIntent(ctx context.Context, client *llm.Client, inv invocation, cands []intent.Candidate, inRepo bool) intent.Decision {
	if inv.Intent != "" {
		n := 0
		if !inv.New && len(cands) > 0 {
			n = 1
		}
		return intent.Decision{
			Intent: intent.Intent(inv.Intent), Language: "en", Reason: "flag",
			ContinuesSession: n,
		}
	}
	d, _ := intent.Classify(ctx, client, inv.Request, cands, inRepo)
	return d
}

func langLine(d intent.Decision) string {
	lang := d.Language
	if lang == "" {
		lang = "en"
	}
	return "Reply in the user's language (" + lang + ")."
}

func thinkingFor(dec intent.Decision, buildThinking string) agent.ThinkingPolicy {
	if dec.Intent == intent.Build {
		return agent.ParseBuildThinking(buildThinking)
	}
	if dec.Reasoning {
		return agent.ThinkOn
	}
	return agent.ThinkOff
}

func runAnswer(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, history []llm.Message, rec agent.Recorder, query, extra string, think bool, inv invocation, dec intent.Decision, routerTime time.Duration) int {
	if rec == nil {
		rec = agent.NopRecorder{}
	}
	system := llm.Message{Role: llm.RoleSystem, Content: extra + "\n\n" + snap.Brief()}
	user := llm.Message{Role: llm.RoleUser, Content: query}
	msgs := []llm.Message{system}
	msgs = append(msgs, sanitizeAnswerHistory(history)...)
	msgs = append(msgs, user)
	_ = rec.Message(ctx, user)

	t0 := time.Now()
	out, usage, streamErr := streamAnswer(ctx, client, msgs, think, inv.Plain)
	if streamErr == nil && strings.TrimSpace(out) == "" {
		out, usage, streamErr = streamAnswer(ctx, client, []llm.Message{system, user}, think, inv.Plain)
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
	if inv.Trace {
		agent.PrintTrace(os.Stderr, 1, think, modelTime, usage.PromptTokens, usage.CompletionTokens, client.ContextWindow(), nil)
	}
	agent.PrintSummary(os.Stderr, string(dec.Intent), st)

	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			return 130
		}
		if errors.Is(streamErr, agent.ErrContextOverflow) {
			fmt.Fprintln(os.Stderr, streamErr.Error())
			return 1
		}
		fmt.Fprintln(os.Stderr, modelErrorLine(inv.Endpoint, streamErr, inv.Trace))
		return 1
	}
	if strings.TrimSpace(out) == "" {
		fmt.Fprintln(os.Stderr, "konsolai: the model returned no answer")
		return 1
	}
	return 0
}

func sanitizeAnswerHistory(hist []llm.Message) []llm.Message {
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

func streamAnswer(ctx context.Context, client *llm.Client, msgs []llm.Message, think, plain bool) (string, llm.Usage, error) {
	var filter llm.ThinkFilter
	var collected strings.Builder
	live := ui.StartLive("thinking")
	first := true
	rend := ui.NewRenderer(os.Stdout, plain)
	if _, err := client.ResolveModel(ctx); err != nil {
		return "", llm.Usage{}, err
	}
	_, maxTok, elide, err := agent.PrepareCall(client, msgs, nil, "answer")
	if err != nil {
		return "", llm.Usage{}, err
	}
	if elide.Count > 0 {
		fmt.Fprintln(os.Stderr, agent.FormatElide(elide, client.ContextWindow()))
	}
	usage, err := client.Stream(ctx, msgs, llm.ChatOptions{
		Thinking: think, Temperature: 0.3, MaxTokens: maxTok,
		OnProgress: func(p llm.Progress) { live.SetLabel(agent.ProgressLabel("thinking", p)) },
	}, func(delta string) error {
		s := filter.Write(delta)
		if first && s != "" {
			live.Stop()
			first = false
			_, _ = io.WriteString(os.Stdout, "\n")
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
	out := trimLeadingBlanks(collected.String())
	if out != "" && !strings.HasSuffix(out, "\n") {
		_, _ = io.WriteString(os.Stdout, "\n")
	}
	return out, usage, err
}

func trimLeadingBlanks(s string) string { return strings.TrimLeft(s, "\r\n") }

type sessionTap struct {
	inner agent.Recorder
	msgs  []llm.Message
}

func (t *sessionTap) Message(ctx context.Context, m llm.Message) error {
	if m.Role != llm.RoleSystem {
		t.msgs = append(t.msgs, m)
		if len(t.msgs) > 20 {
			t.msgs = t.msgs[len(t.msgs)-20:]
		}
	}
	if t.inner != nil {
		return t.inner.Message(ctx, m)
	}
	return nil
}

func (t *sessionTap) Action(ctx context.Context, a agent.ActionRecord) error {
	if t.inner != nil {
		return t.inner.Action(ctx, a)
	}
	return nil
}

func (t *sessionTap) FileChange(ctx context.Context, c tools.FileChange) error {
	if t.inner != nil {
		return t.inner.FileChange(ctx, c)
	}
	return nil
}

func runProfile(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, facts string, history []llm.Message, rec agent.Recorder, mem *memory.Guard, sessionID string, projectID *int64, prof config.Profile, inv invocation, cwd string, prompter *ui.Prompter, dec intent.Decision, routerTime time.Duration) int {
	defer prompter.Close()
	lang := langLine(dec)
	if dec.Intent == intent.Answer {
		return runAnswer(ctx, client, snap, history, rec, inv.Request, agent.AnswerPrompt+"\n"+lang, dec.Reasoning, inv, dec, routerTime)
	}

	root := repo.Root(cwd)
	snap.RepoRoot = root
	sg, err := safety.NewAt(root, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	lister := func(ctx context.Context) ([]string, error) {
		return repo.Files(ctx, root)
	}
	toolOut := io.Writer(os.Stdout)
	if !inv.Plain {
		toolOut = ui.NewGutter(os.Stdout)
	}
	reg := tools.NewRegistry(sg, toolOut, lister, memory.NewWebCache(mem), memory.NewFactStore(mem, prof.Name, projectID, sessionID), tools.Options{
		ReadOnlyShell: dec.Intent == intent.Lookup,
		DocsTask:      dec.Task == intent.TaskDocs,
		Request:       inv.Request,
	})
	cfg := agent.Config{
		Client:         client,
		Tools:          reg,
		Confirmer:      prompter,
		Out:            os.Stdout,
		Recorder:       rec,
		History:        history,
		AutoApprove:    inv.Yes,
		Prompt:         agent.ActPrompt + "\n" + lang,
		ThinkingPolicy: thinkingFor(dec, prof.BuildThinking),
		Trace:          inv.Trace,
		SessionID:      sessionID,
		Intent:         string(dec.Intent),
		RouterTime:     routerTime,
		Err:            os.Stderr,
		Plain:          inv.Plain,
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

	err = agent.Run(ctx, cfg, sys, inv.Request)
	if dec.Intent == intent.Build {
		printDiffStat(root)
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, agent.ErrAborted) {
		fmt.Fprintln(os.Stderr, "konsolai: aborted")
		return 3
	}
	if errors.Is(err, agent.ErrMaxSteps) {
		fmt.Fprintf(os.Stderr, "konsolai: stopped after %d steps without finishing\n", cfg.MaxSteps)
		return 4
	}
	if errors.Is(err, agent.ErrNoAnswer) {
		fmt.Fprintln(os.Stderr, "konsolai: the model returned no answer")
		return 1
	}
	if errors.Is(err, agent.ErrContextOverflow) {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	fmt.Fprintln(os.Stderr, modelErrorLine(inv.Endpoint, err, inv.Trace))
	return 1
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
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		r = filepath.Clean(root)
	}
	return c != r
}

func printDiffStat(root string) {
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
	fmt.Fprintln(os.Stdout, ui.Dim(s))
}

func modelErrorLine(endpoint string, err error, trace bool) string {
	if err == nil {
		return "konsolai: unknown error"
	}
	if trace {
		return "konsolai: " + err.Error()
	}
	host := endpointHost(endpoint)
	if errors.Is(err, llm.ErrIdleTimeout) {
		return "konsolai: the model server stopped responding (" + host + "). Try again, or check the server."
	}
	if errors.Is(err, llm.ErrTruncated) {
		return "konsolai: the model's answer was cut off; try a narrower request."
	}
	var he *llm.HTTPError
	if errors.As(err, &he) && he.Status >= 400 && he.Status < 500 {
		return "konsolai: the model server rejected the request: " + firstErrLine(he.Body)
	}
	if isDialErr(err) {
		return "konsolai: cannot reach the model server at " + host + "."
	}
	return "konsolai: " + err.Error()
}

func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err == nil && u.Host != "" {
		return u.Host
	}
	s := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "the model server"
	}
	return s
}

func firstErrLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isDialErr(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "no such host") || strings.Contains(s, "dial tcp")
}
