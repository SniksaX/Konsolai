package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/turn"
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

	client := llm.New(prof.Endpoint, prof.Model)
	client.SetIdleTimeout(time.Duration(prof.LLMIdleTimeoutSeconds) * time.Second)
	res := (&turn.Runner{
		Profile: prof,
		Client:  client,
		Mem:     mem,
		Out:     os.Stdout,
		Err:     os.Stderr,
	}).RunTurn(ctx, turn.Request{
		Text: inv.Request, Intent: inv.Intent, Yes: inv.Yes,
		Trace: inv.Trace, Plain: inv.Plain, New: inv.New, Cwd: cwd,
	})
	return res.ExitCode
}

func upsertProject(ctx context.Context, mem *memory.Guard, cwd string) *int64 {
	return turn.UpsertProject(ctx, mem, cwd)
}

type resumeCandidate = turn.ResumeCandidate

func findCandidates(ctx context.Context, mem *memory.Guard, prof config.Profile, projectID *int64, cwd string) []resumeCandidate {
	return turn.FindCandidates(ctx, mem, prof, projectID, cwd)
}

func applyResume(inv invocation, dec intent.Decision, cands []resumeCandidate) resumeCandidate {
	return turn.ApplyResume(inv.New, dec, cands)
}

func thinkingFor(dec intent.Decision, buildThinking string) agent.ThinkingPolicy {
	return turn.ThinkingFor(dec, buildThinking)
}

func sanitizeAnswerHistory(hist []llm.Message) []llm.Message {
	return turn.SanitizeAnswerHistory(hist)
}

func streamAnswer(ctx context.Context, client *llm.Client, msgs []llm.Message, think, plain bool) (string, llm.Usage, error) {
	return turn.StreamAnswer(ctx, client, msgs, think, plain, os.Stdout, os.Stderr)
}

func modelErrorLine(endpoint string, err error, trace bool) string {
	return turn.ModelErrorLine(endpoint, err, trace)
}
