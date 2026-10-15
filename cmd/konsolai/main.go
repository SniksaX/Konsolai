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
	"strings"
	"syscall"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/sysctx"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

var _ agent.Confirmer = (*ui.Prompter)(nil)

const (
	defaultEndpoint = "http://fatboy:11436/v1"
	usageText       = `usage: konsolai [--yes] [howto|do|code] "<request>"`

	howtoSystemPrompt = `You are Konsolai, a terminal assistant running inside the user's shell.
Answer with the exact command(s) that accomplish the request in a single bash code block,
then at most 3 short bullet points explaining non-obvious flags.
Prefer tools present on a default install of the user's OS. Use paths relative to the current directory.
If the request is destructive or ambiguous, say so in one line. No preamble, no closing remarks.`
)

type mode int

const (
	modeDo mode = iota
	modeHowto
	modeCode
)

type invocation struct {
	Mode    mode
	Request string
	Yes     bool
}

var errRequest = errors.New(`request must be a single argument in double quotes, e.g. konsolai code "add a /health endpoint"`)

func parseArgs(args []string) (invocation, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("konsolai", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "auto-approve non-denylisted actions")
	if err := fs.Parse(args); err != nil {
		return invocation{}, err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return invocation{}, errRequest
	}
	inv := invocation{Mode: modeDo, Yes: *yes}
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
	if len(rest) != 1 {
		return invocation{}, errRequest
	}
	if strings.TrimSpace(rest[0]) == "" {
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

	snap, err := sysctx.Collect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	client := llm.New(envOr("KONSOLAI_ENDPOINT", defaultEndpoint), os.Getenv("KONSOLAI_MODEL"))

	if inv.Mode == modeHowto {
		return runHowto(ctx, client, snap, inv.Request)
	}
	return runAgent(ctx, client, snap, inv)
}

func runHowto(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, query string) int {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: howtoSystemPrompt + "\n\n" + snap.String()},
		{Role: llm.RoleUser, Content: query},
	}

	var filter llm.ThinkFilter
	streamErr := client.Stream(ctx, msgs, llm.ChatOptions{Thinking: false, Temperature: 0.2}, func(delta string) error {
		_, err := io.WriteString(os.Stdout, filter.Write(delta))
		return err
	})
	_, _ = io.WriteString(os.Stdout, filter.Flush()+"\n")

	if streamErr != nil {
		if errors.Is(streamErr, context.Canceled) {
			return 130
		}
		fmt.Fprintln(os.Stderr, "konsolai:", streamErr)
		return 1
	}
	return 0
}

func runAgent(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, inv invocation) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	root := cwd
	if inv.Mode == modeCode {
		root = repo.Root(cwd)
	}
	guard, err := safety.New(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	prompter, err := ui.OpenPrompter(inv.Yes)
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
		Tools:               tools.NewRegistry(guard, os.Stdout, lister),
		Confirmer:           prompter,
		Out:                 os.Stdout,
		ContextBudgetTokens: 60000,
	}
	sys := snap.String()
	if inv.Mode == modeCode {
		cfg.Prompt = agent.CodePrompt
		cfg.Thinking = true
		cfg.Temperature = 0.3
		cfg.MaxSteps = 60
		cfg.RequirePlan = true
		sys = codeContext(ctx, snap, root)
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
