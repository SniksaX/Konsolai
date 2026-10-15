package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/llm"
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
	return runAgent(ctx, client, snap, inv.Request, inv.Yes)
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

func runAgent(ctx context.Context, client *llm.Client, snap sysctx.Snapshot, task string, yes bool) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	guard, err := safety.New(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	prompter, err := ui.OpenPrompter(yes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "konsolai:", err)
		return 1
	}
	defer prompter.Close()

	err = agent.Run(ctx, agent.Config{
		Client:    client,
		Tools:     tools.NewRegistry(guard, os.Stdout, nil),
		Confirmer: prompter,
		Out:       os.Stdout,
		MaxSteps:  15,
		Thinking:  false,
	}, snap.String(), task)
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
