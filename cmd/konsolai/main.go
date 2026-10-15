package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/sysctx"
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
	query := stripMode(strings.TrimSpace(strings.Join(args, " ")))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: @howto <question>")
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

// stripMode removes the "howto" or "?" mode prefix. All modes route to howto until the agent loop exists.
func stripMode(q string) string {
	if rest, ok := strings.CutPrefix(q, "?"); ok {
		return strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(q, "howto"); ok && (rest == "" || rest[0] == ' ') {
		return strings.TrimSpace(rest)
	}
	return q
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
