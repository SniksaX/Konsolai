package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/intent"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    invocation
		wantErr error
	}{
		{
			name: "request only",
			args: []string{"create a folder"},
			want: invocation{Request: "create a folder"},
		},
		{
			name: "intent flag",
			args: []string{"--intent", "build", "add tests"},
			want: invocation{Intent: "build", Request: "add tests"},
		},
		{
			name: "yes new",
			args: []string{"--yes", "new", "hello"},
			want: invocation{Request: "hello", Yes: true, New: true},
		},
		{
			name: "no request",
			args: nil,
			want: invocation{},
		},
		{
			name: "new only",
			args: []string{"new"},
			want: invocation{New: true},
		},
		{
			name: "history",
			args: []string{"history"},
			want: invocation{Cmd: cmdHistory},
		},
		{
			name: "remember project",
			args: []string{"remember", "--project", "I use pnpm"},
			want: invocation{Cmd: cmdRemember, Fact: "I use pnpm", FactScope: "project"},
		},
		{
			name: "forget user",
			args: []string{"forget", "user", "3"},
			want: invocation{Cmd: cmdForget, FactScope: "user", FactID: 3},
		},
		{
			name: "profile",
			args: []string{"--profile", "home", "new", "hey"},
			want: invocation{Request: "hey", Profile: "home", New: true},
		},
		{
			name:    "bad intent",
			args:    []string{"--intent", "nope", "x"},
			wantErr: errRequest,
		},
		{
			name:    "extra args",
			args:    []string{"a", "b"},
			wantErr: errRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v want %+v", got, tt.want)
			}
		})
	}
}

func TestParseArgsHelp(t *testing.T) {
	_, err := parseArgs([]string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v", err)
	}
}

func TestResolveRequestCombinations(t *testing.T) {
	t.Cleanup(func() {
		stdinIsTTY = defaultStdinTTY
		readRawTTY = defaultReadTTY
		readStdinN = defaultReadStdin
	})

	t.Run("tty no request", func(t *testing.T) {
		stdinIsTTY = func() bool { return true }
		readRawTTY = func() (string, error) { return "from tty\n", nil }
		got, err := resolveRequest(invocation{})
		if err != nil || got.Request != "from tty" {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("tty empty", func(t *testing.T) {
		stdinIsTTY = func() bool { return true }
		readRawTTY = func() (string, error) { return "  \n", nil }
		_, err := resolveRequest(invocation{})
		if !errors.Is(err, errRequest) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("pipe is request", func(t *testing.T) {
		stdinIsTTY = func() bool { return false }
		readStdinN = func(int) (string, error) { return "piped\n", nil }
		got, err := resolveRequest(invocation{})
		if err != nil || got.Request != "piped" {
			t.Fatalf("%+v %v", got, err)
		}
	})
	t.Run("arg plus stdin context", func(t *testing.T) {
		stdinIsTTY = func() bool { return false }
		readStdinN = func(int) (string, error) { return "kill: usage\n", nil }
		got, err := resolveRequest(invocation{Request: "fix this"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Request, "fix this") || !strings.Contains(got.Request, "<stdin>") || !strings.Contains(got.Request, "kill: usage") {
			t.Fatalf("%q", got.Request)
		}
	})
	t.Run("arg tty ignores stdin", func(t *testing.T) {
		stdinIsTTY = func() bool { return true }
		readStdinN = func(int) (string, error) { t.Fatal("stdin read"); return "", nil }
		got, err := resolveRequest(invocation{Request: "hello"})
		if err != nil || got.Request != "hello" {
			t.Fatalf("%+v %v", got, err)
		}
	})
}

func TestConfirmUsesTTYNotStdin(t *testing.T) {
	// Confirmer reads /dev/tty (or auto-approves). Piped stdin must not be
	// consumed by confirm; resolveRequest attaches it as context instead.
	stdinIsTTY = func() bool { return false }
	readStdinN = func(int) (string, error) { return "context from pipe", nil }
	t.Cleanup(func() {
		stdinIsTTY = defaultStdinTTY
		readStdinN = defaultReadStdin
	})
	got, err := resolveRequest(invocation{Request: "do something"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Request, "<stdin>") {
		t.Fatalf("%q", got.Request)
	}
}

func TestThinkingFor(t *testing.T) {
	if g := thinkingFor(intent.Decision{Intent: intent.Build}); g != agent.ThinkUntilPlan {
		t.Fatalf("build %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Answer, Reasoning: true}); g != agent.ThinkOn {
		t.Fatalf("answer+reason %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Lookup, Reasoning: false}); g != agent.ThinkOff {
		t.Fatalf("lookup %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Act, Reasoning: true}); g != agent.ThinkOn {
		t.Fatalf("act+reason %v", g)
	}
}
