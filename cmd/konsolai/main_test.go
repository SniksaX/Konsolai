package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
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
			name: "trace",
			args: []string{"--trace", "new", "hello"},
			want: invocation{Request: "hello", New: true, Trace: true},
		},
		{
			name: "plain",
			args: []string{"--plain", "hello"},
			want: invocation{Request: "hello", Plain: true},
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

func TestSanitizeAnswerHistory(t *testing.T) {
	hist := []llm.Message{
		{Role: llm.RoleTool, Content: "tool"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1"}}},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
	}
	for i := 0; i < 14; i++ {
		hist = append(hist, llm.Message{Role: llm.RoleUser, Content: "x"})
	}
	got := sanitizeAnswerHistory(hist)
	if len(got) != 12 {
		t.Fatalf("len %d", len(got))
	}
	for _, m := range got {
		if m.Role == llm.RoleTool {
			t.Fatal("tool kept")
		}
		if m.Role == llm.RoleAssistant && m.Content == "" && len(m.ToolCalls) > 0 {
			t.Fatal("tool_calls-only kept")
		}
	}
}

func TestStreamAnswerRetryThenEmpty(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "qwen")
	out, _, err := streamAnswer(context.Background(), c, []llm.Message{{Role: llm.RoleUser, Content: "q"}}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("first %q", out)
	}
	out, _, err = streamAnswer(context.Background(), c, []llm.Message{{Role: llm.RoleUser, Content: "q"}}, false, true)
	if err != nil || out != "hi" {
		t.Fatalf("retry %q %v", out, err)
	}
}

func TestResumeSessionLocalFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := "/tmp/kf"
	if err := memory.SaveLast(memory.LastSession{
		Profile:  "default",
		CWD:      cwd,
		Title:    "quel process",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "port 3000"}},
	}); err != nil {
		t.Fatal(err)
	}
	mem := memory.NewGuard(nil, nil)
	prof := config.Profile{Name: "default", SessionIdleMinutes: 30}
	id, hist := resumeSession(context.Background(), mem, prof, nil, cwd, invocation{})
	if id != "" || len(hist) != 1 || hist[0].Content != "port 3000" {
		t.Fatalf("%q %+v", id, hist)
	}
	_, hist = resumeSession(context.Background(), mem, prof, nil, cwd, invocation{New: true})
	if len(hist) != 0 {
		t.Fatalf("new should ignore local: %+v", hist)
	}
}

func TestModelErrorLine(t *testing.T) {
	ep := "http://fatboy:11436/v1"
	if got := modelErrorLine(ep, llm.ErrIdleTimeout, false); !strings.Contains(got, "stopped responding (fatboy:11436)") {
		t.Fatalf("idle %q", got)
	}
	if got := modelErrorLine(ep, llm.ErrTruncated, false); !strings.Contains(got, "cut off") {
		t.Fatalf("trunc %q", got)
	}
	he := &llm.HTTPError{Status: 400, Body: "bad schema\nmore"}
	if got := modelErrorLine(ep, he, false); got != "konsolai: the model server rejected the request: bad schema" {
		t.Fatalf("4xx %q", got)
	}
	dial := errors.New("llm: chat: dial tcp: connection refused")
	if got := modelErrorLine(ep, dial, false); got != "konsolai: cannot reach the model server at fatboy:11436." {
		t.Fatalf("dial %q", got)
	}
	if got := modelErrorLine(ep, llm.ErrIdleTimeout, true); !strings.Contains(got, llm.ErrIdleTimeout.Error()) {
		t.Fatalf("trace %q", got)
	}
}
