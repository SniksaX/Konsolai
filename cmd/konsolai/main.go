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

const (
	defaultEndpoint = "http://fatboy:11436/v1"

	howtoSystemPrompt = `You are Konsolai, a terminal assistant running inside the user's shell.
Answer with the exact command(s) that accomplish the request in a single bash code block,
then at most 3 short bullet points explaining non-obvious flags.
Prefer tools present on a default install of the user's OS. Use paths relative to the current directory.
If the request is destructive or ambiguous, say so in one line. No preamble, no closing remarks.`
)

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("konsolai", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes := fs.Bool("yes", false, "auto-approve non-denylisted actions")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: konsolai [--yes] -- <task>")
		return 2
	}

	howto, rest := splitMode(query)
	if rest == "" {
		fmt.Fprintln(os.Stderr, "usage: konsolai [--yes] -- <task>")
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

	if howto {
		return runHowto(ctx, client, snap, rest)
	}
	return runAgent(ctx, client, snap, rest, *yes)
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
		Client:   client,
		Tools:    tools.NewRegistry(guard, os.Stdout),
		Prompter: prompter,
		Out:      os.Stdout,
		MaxSteps: 15,
		Thinking: false,
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

// splitMode reports whether q is a howto query and the remaining text.
// "howto ..." and "?..." are howto. "do ..." strips the prefix and is agent. Anything else is agent.
func splitMode(q string) (howto bool, rest string) {
	if rest, ok := strings.CutPrefix(q, "?"); ok {
		return true, strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(q, "howto"); ok && (rest == "" || rest[0] == ' ') {
		return true, strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(q, "do"); ok && (rest == "" || rest[0] == ' ') {
		return false, strings.TrimSpace(rest)
	}
	return false, q
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
