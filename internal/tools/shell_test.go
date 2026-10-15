package tools

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
)

func shellToolFor(t *testing.T) Tool {
	t.Helper()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	return tool
}

func TestShellExitCode(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"exit 2"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("want IsError")
	}
	if res.Meta != "exit 2" {
		t.Fatalf("Meta %q", res.Meta)
	}
	if !strings.Contains(res.Output, "exit_code: 2") {
		t.Fatalf("Output %q", res.Output)
	}
}

func TestShellTimeout(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"sleep 5","timeout_sec":1}`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout took too long")
	}
	if !res.IsError {
		t.Fatal("want IsError")
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Fatalf("Output %q", res.Output)
	}
	if res.Meta != "timed out 1s" {
		t.Fatalf("Meta %q", res.Meta)
	}
}

func TestShellCancelKillsGrandchild(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"sleep 30 & wait"}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := act.Run(ctx)
		done <- runErr
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("got %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not return quickly")
	}
}
