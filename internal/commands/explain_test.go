package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/turn"
)

func TestExplainCapturedFailure(t *testing.T) {
	env, out, errb := testEnv(t)
	env.FailedShell = func() (string, int, string, bool) {
		return "false", 1, "boom\n", true
	}
	var got turn.Request
	env.RunTurn = func(_ context.Context, req turn.Request) turn.Result {
		got = req
		return turn.Result{ExitCode: 0}
	}
	if code := runExplain(context.Background(), env, nil); code != 0 {
		t.Fatalf("%d %s %s", code, out.String(), errb.String())
	}
	if got.Intent != "answer" || got.Effort != "high" {
		t.Fatalf("%+v", got)
	}
	for _, want := range []string{"Command: false", "Exit code: 1", "boom", "Explain why it failed and give the fix."} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("missing %q in %q", want, got.Text)
		}
	}
}

func TestExplainNothingCaptured(t *testing.T) {
	env, out, errb := testEnv(t)
	t.Setenv("HISTFILE", "")
	env.FailedShell = func() (string, int, string, bool) { return "", 0, "", false }
	called := false
	env.RunTurn = func(context.Context, turn.Request) turn.Result {
		called = true
		return turn.Result{}
	}
	if code := runExplain(context.Background(), env, nil); code != 1 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if called {
		t.Fatal("ran a turn")
	}
	if strings.TrimSpace(out.String()) != "no failed command to explain" {
		t.Fatalf("%q", out.String())
	}
}

func TestExplainHistfileFallback(t *testing.T) {
	env, out, errb := testEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")
	if err := os.WriteFile(path, []byte("echo ok\nls missing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HISTFILE", path)
	var got turn.Request
	env.RunTurn = func(_ context.Context, req turn.Request) turn.Result {
		got = req
		return turn.Result{ExitCode: 0}
	}
	if code := runExplain(context.Background(), env, nil); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "no captured output; explaining the command only") {
		t.Fatalf("%q", out.String())
	}
	if !strings.Contains(got.Text, "ls missing") || !strings.Contains(got.Text, "Explain why it failed and give the fix.") {
		t.Fatalf("%q", got.Text)
	}
	if got.Intent != "answer" || got.Effort != "high" {
		t.Fatalf("%+v", got)
	}
}
