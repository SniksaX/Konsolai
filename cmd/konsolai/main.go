package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	usageText = `usage: konsolai [--yes] [--profile NAME] [new] [howto|do|code] "<request>"`

	howtoSystemPrompt = `You are Konsolai, a terminal assistant running inside the user's shell.
Answer with the exact command(s) that accomplish the request in a single bash code block,
then at most 3 short bullet points explaining non-obvious flags.
Prefer tools present on a default install of the user's OS. Use paths relative to the current directory.
If the request is destructive or ambiguous, say so in one line. No preamble, no closing remarks.`

	noMemory = "memory is not configured (see ~/.config/konsolai/config.json)"
)

type mode int

const (
	modeDo mode = iota
	modeHowto
	modeCode
)

func (m mode) String() string {
	switch m {
	case modeHowto:
		return "howto"
	case modeCode:
		return "code"
	default:
		return "do"
	}
}

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
	Mode      mode
	Request   string
	Yes       bool
	Profile   string
	New       bool
	Cmd       command
	FactScope string
	FactID    int64
	Fact      string
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
	project := fs.Bool("project", false, "project-scoped fact")
	if err := fs.Parse(args); err != nil {
		return invocation{}, err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return invocation{}, errRequest
	}
	inv := invocation{Mode: modeDo, Yes: *yes, Profile: *profile}
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
		if len(rest) == 0 {
			return invocation{}, errRequest
		}
	}
	switch rest[0] {
	case "howto":
		inv.Mode = modeHowto
		rest = rest[1:]
	case "do":
		inv.Mode = modeDo
		rest = rest[1:]
	case "code":
		inv.Mode = modeCode
		rest = rest[1:]
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
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

	needMem := inv.Cmd != cmdRequest
	var st *store.Store
	if prof.DatabaseURL == "" {
		if needMem {
			fmt.Fprintln(os.Stderr, noMemory)
			return 1
		}
	} else {
		clear := ui.Status(os.Stderr, "loading memory")
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

	snap, err := sysctx.Collect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	client := llm.New(prof.Endpoint, prof.Model)
	facts := loadFactsBlock(ctx, mem, prof.Name, projectID)

	var code int
	var sessionID string
	var history []llm.Message
	if inv.Mode == modeHowto {
		sessionID, history = resumeOrCreate(ctx, mem, prof, projectID, cwd, inv)
		rec := memory.NewRecorder(mem, sessionID)
		code = runHowto(ctx, client, snap, facts, history, rec, inv.Request)
	} else {
		prompter, err := ui.OpenPrompter(inv.Yes)
		if err != nil {
			fmt.Fprintln(os.Stderr, "konsolai:", err)
			return 1
		}
		sessionID, history = resumeOrCreate(ctx, mem, prof, projectID, cwd, inv)
		rec := memory.NewRecorder(mem, sessionID)
		code = runAgent(ctx, client, snap, facts, history, rec, mem, sessionID, projectID, prof.Name, inv, cwd, prompter)
	}
	if sessionID != "" {
		if s := mem.Store(); s != nil {
			if err := s.EndSession(ctx, sessionID, sessionStatus(code)); err != nil {
				mem.Fail(err)
			}
		}
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

func resumeOrCreate(ctx context.Context, mem *memory.Guard, prof config.Profile, projectID *int64, cwd string, inv invocation) (string, []llm.Message) {
	if mem.Store() == nil {
		return "", nil
	}
	idle := time.Duration(prof.SessionIdleMinutes) * time.Minute
	if !inv.New {
		sess, found, err := mem.Store().FindResumableSession(ctx, prof.Name, projectID, cwd, inv.Mode.String(), idle)
		if err != nil {
			mem.Fail(err)
			return "", nil
		}
		if found {
			msgs, err := mem.Store().LoadMessages(ctx, sess.ID)
			if err != nil {
				mem.Fail(err)
				return "", nil
			}
			fmt.Fprintln(os.Stdout, ui.Dim("↻ continuing session: "+sess.Title))
			return sess.ID, memory.ToLLM(msgs)
		}
	}
	title := inv.Request
	if utf8.RuneCountInString(title) > 80 {
		title = string([]rune(title)[:80])
	}
	s := mem.Store()
	if s == nil {
		return "", nil
	}
	id, err := s.CreateSession(ctx, store.NewSession{
		Profile: prof.Name, ProjectID: projectID, CWD: cwd,
		Mode: inv.Mode.String(), Model: prof.Model, Title: title,
	})
	if err != nil {
		mem.Fail(err)
		return "", nil
	}
	return id, nil
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

func runHowto(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, facts string, history []llm.Message, rec agent.Recorder, query string) int {
	if rec == nil {
		rec = agent.NopRecorder{}
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: howtoSystemPrompt + "\n\n" + snap.String() + facts},
	}
	msgs = append(msgs, history...)
	user := llm.Message{Role: llm.RoleUser, Content: query}
	msgs = append(msgs, user)
	_ = rec.Message(ctx, user)

	var filter llm.ThinkFilter
	var collected strings.Builder
	streamErr := client.Stream(ctx, msgs, llm.ChatOptions{Thinking: false, Temperature: 0.2}, func(delta string) error {
		s := filter.Write(delta)
		collected.WriteString(s)
		_, err := io.WriteString(os.Stdout, s)
		return err
	})
	rest := filter.Flush()
	collected.WriteString(rest)
	_, _ = io.WriteString(os.Stdout, rest+"\n")
	_ = rec.Message(ctx, llm.Message{Role: llm.RoleAssistant, Content: collected.String()})

	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			return 130
		}
		fmt.Fprintln(os.Stderr, "konsolai:", streamErr)
		return 1
	}
	return 0
}

func runAgent(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, facts string, history []llm.Message, rec agent.Recorder, mem *memory.Guard, sessionID string, projectID *int64, profile string, inv invocation, cwd string, prompter *ui.Prompter) int {
	root := cwd
	if inv.Mode == modeCode {
		root = repo.Root(cwd)
	}
	sg, err := safety.New(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	defer prompter.Close()

	lister := func(ctx context.Context) ([]string, error) {
		return repo.Files(ctx, root)
	}
	cfg := agent.Config{
		Client:              client,
		Tools:               tools.NewRegistry(sg, os.Stdout, lister, memory.NewWebCache(mem), memory.NewFactStore(mem, profile, projectID, sessionID)),
		Confirmer:           prompter,
		Out:                 os.Stdout,
		ContextBudgetTokens: 60000,
		Recorder:            rec,
		History:             history,
		AutoApprove:         inv.Yes,
	}
	sys := snap.String() + facts
	if inv.Mode == modeCode {
		cfg.Prompt = agent.CodePrompt
		cfg.Thinking = true
		cfg.Temperature = 0.3
		cfg.MaxSteps = 60
		cfg.RequirePlan = true
		sys = codeContext(ctx, snap, root) + facts
	} else {
		cfg.Thinking = false
		cfg.MaxSteps = 15
	}

	err = agent.Run(ctx, cfg, sys, inv.Request)
	if inv.Mode == modeCode {
		printDiffStat(root)
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, agent.ErrAborted) {
		return 3
	}
	if errors.Is(err, agent.ErrMaxSteps) {
		return 4
	}
	fmt.Fprintln(os.Stderr, "konsolai:", err)
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
