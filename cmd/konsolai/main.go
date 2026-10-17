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
	"github.com/siraj/konsolai/internal/commands"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/embed"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/rag"
	"github.com/siraj/konsolai/internal/repl"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/turn"
	"github.com/siraj/konsolai/internal/ui"
)

var _ agent.Confirmer = (*ui.Prompter)(nil)

var version = "dev"

const (
	usageText = `usage: konsolai [--yes] [--profile NAME] [--intent answer|lookup|act|build] [--trace] [--plain] [--version] [new] ["<request>"]
       konsolai /<command> [args]`

	noMemory = "memory is not configured (see ~/.config/konsolai/config.yaml)"
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
	Version   bool
	Slash     string
	SlashArgs [8]string
	SlashN    int
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
	showVer := fs.Bool("version", false, "print version and exit")
	project := fs.Bool("project", false, "project-scoped fact")
	if err := fs.Parse(args); err != nil {
		return invocation{}, err
	}
	if *intent != "" && *intent != "answer" && *intent != "lookup" && *intent != "act" && *intent != "build" {
		return invocation{}, errRequest
	}
	rest := fs.Args()
	inv := invocation{Yes: *yes, Profile: *profile, Intent: *intent, Trace: *trace, Plain: *plain, Version: *showVer}
	if len(rest) == 0 {
		return inv, nil
	}
	if strings.HasPrefix(rest[0], "/") {
		inv.Slash = strings.TrimPrefix(rest[0], "/")
		args := rest[1:]
		if *project {
			args = append([]string{"--project"}, args...)
		}
		if len(args) > 8 {
			args = args[:8]
		}
		inv.SlashN = len(args)
		copy(inv.SlashArgs[:], args)
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
	if inv.Version {
		fmt.Println(version)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	file, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	if file.Notice != "" {
		fmt.Fprintln(os.Stderr, ui.Current().Muted(file.Notice))
	}
	prof, err := file.Resolve(inv.Profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	ui.ApplyTheme(prof.PaletteName(), inv.Plain)
	inv.Endpoint = prof.Endpoint

	needMem := inv.Cmd == cmdUndo || inv.Cmd == cmdFacts || inv.Cmd == cmdPrune || inv.Cmd == cmdRemember || inv.Cmd == cmdForget
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

	client := llm.New(prof.Endpoint, prof.Model)
	client.SetIdleTimeout(time.Duration(prof.LLMIdleTimeoutSeconds) * time.Second)
	runner := &turn.Runner{
		Profile:    prof,
		Client:     client,
		FastClient: llm.NewFast(prof.FastEndpoint, prof.FastModel),
		Mem:        mem,
		Out:        os.Stdout,
		Err:        os.Stderr,
		Version:    version,
		SlashHelp:  commands.Default().SlashLines(),
	}
	isRepl := inv.Slash == "" && inv.Cmd == cmdRequest && strings.TrimSpace(inv.Request) == "" && stdinIsTTY() && stdoutIsTTY()
	var emb *embed.Client
	if st != nil && prof.RAGEnabled() {
		if !st.RAGAvailable(ctx) {
			fmt.Fprintln(os.Stderr, ui.Current().Warn("rag unavailable"))
		} else {
			emb = embed.New(prof.EmbedEndpoint, prof.RerankEndpoint)
			emb.SetBatch(prof.RAG.EmbedBatch)
			ing := rag.New(st, emb, prof.Name)
			ing.SetTune(rag.TuneFrom(prof))
			ing.SetNotice(os.Stderr)
			runner.Ingest = ing
			runner.Embed = emb
			if isRepl {
				ing.Start()
				defer ing.Stop()
			} else if inv.Slash == "" && inv.Cmd == cmdRequest {
				defer func() {
					n := ing.Flush(context.Background(), rag.FlushCap)
					if inv.Trace && n > 0 {
						fmt.Fprintf(os.Stderr, "rag: dropped %d pending items\n", n)
					}
				}()
			}
		}
	}

	if isRepl {
		env := commandEnv(runner, mem, prof, cwd, projectID, inv, emb)
		env.InRepl = true
		model := prof.Model
		if id, err := client.ResolveModel(ctx); err == nil && id != "" {
			model = id
		}
		return repl.Run(ctx, repl.Config{
			Runner: runner, Env: env, Reg: commands.Default(),
			Version: version, Model: model, Out: os.Stdout, Err: os.Stderr,
			Trace: inv.Trace,
		})
	}

	if inv.Slash != "" || inv.Cmd != cmdRequest {
		env := commandEnv(runner, mem, prof, cwd, projectID, inv, emb)
		name, args := inv.Slash, inv.SlashArgs[:inv.SlashN]
		if inv.Slash == "" {
			name, args = bareCommand(inv)
			commands.NoteUseSlash(os.Stderr, name)
		}
		return commands.Default().Dispatch(ctx, env, name, args)
	}

	inv, err = resolveRequest(inv)
	if err != nil {
		fmt.Fprintln(os.Stderr, usageText)
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()
	res := runner.RunTurn(turnCtx, turn.Request{
		Text: inv.Request, Intent: inv.Intent, Yes: inv.Yes,
		Trace: inv.Trace, Plain: inv.Plain, New: inv.New, Cwd: cwd,
	})
	return res.ExitCode
}

func commandEnv(runner *turn.Runner, mem *memory.Guard, prof config.Profile, cwd string, projectID *int64, inv invocation, emb *embed.Client) *commands.Env {
	return &commands.Env{
		Runner:     runner,
		Mem:        mem,
		Profile:    prof,
		Out:        os.Stdout,
		Err:        os.Stderr,
		Cwd:        cwd,
		Yes:        inv.Yes,
		Version:    version,
		Plain:      inv.Plain,
		Embed:      emb,
		ProjectID:  projectID,
		ConfigPath: config.DefaultPath(),
		Undo: func(ctx context.Context) int {
			return runUndo(ctx, mem, projectID, cwd, inv.Yes)
		},
		Remember: func(ctx context.Context, args []string) int {
			scope := "user"
			if len(args) > 0 && args[0] == "--project" {
				scope = "project"
				args = args[1:]
			}
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				fmt.Fprintln(os.Stderr, `usage: /remember [--project] "<fact>"`)
				return 2
			}
			return runRemember(ctx, mem, prof.Name, projectID, args[0], scope)
		},
		Facts: func(ctx context.Context) int {
			return runFacts(ctx, mem, prof.Name, projectID)
		},
		Forget: func(ctx context.Context, args []string) int {
			if len(args) != 2 {
				fmt.Fprintln(os.Stderr, "usage: /forget user|project <id>")
				return 2
			}
			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil || id < 1 {
				fmt.Fprintln(os.Stderr, "usage: /forget user|project <id>")
				return 2
			}
			return runForget(ctx, mem, args[0], id)
		},
		Prune: func(ctx context.Context) int {
			return runPrune(ctx, mem, prof.RetentionDays)
		},
	}
}

func bareCommand(inv invocation) (string, []string) {
	switch inv.Cmd {
	case cmdHistory:
		return "history", nil
	case cmdUndo:
		return "undo", nil
	case cmdFacts:
		return "facts", nil
	case cmdPrune:
		return "prune", nil
	case cmdRemember:
		if inv.FactScope == "project" {
			return "remember", []string{"--project", inv.Fact}
		}
		return "remember", []string{inv.Fact}
	case cmdForget:
		return "forget", []string{inv.FactScope, strconv.FormatInt(inv.FactID, 10)}
	default:
		return "", nil
	}
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
	return turn.ThinkingFor(dec, buildThinking, turn.ThinkAuto)
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
