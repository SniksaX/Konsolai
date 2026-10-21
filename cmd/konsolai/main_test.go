package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/turn"
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

func TestParseArgsSlashAndVersion(t *testing.T) {
	got, err := parseArgs([]string{"--version"})
	if err != nil || !got.Version || got.Request != "" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = parseArgs([]string{"/copy", "answer", "2"})
	if err != nil || got.Slash != "copy" || got.SlashN != 2 || got.SlashArgs[0] != "answer" || got.SlashArgs[1] != "2" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = parseArgs([]string{"/resume", "3", "and then?"})
	if err != nil || got.Slash != "resume" || got.SlashN != 2 || got.SlashArgs[1] != "and then?" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = parseArgs([]string{"history"})
	if err != nil || got.Cmd != cmdHistory || got.Slash != "" {
		t.Fatalf("bare history %+v %v", got, err)
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
	if g := thinkingFor(intent.Decision{Intent: intent.Build}, ""); g != agent.ThinkPlanStep {
		t.Fatalf("build default %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Build}, "until_plan"); g != agent.ThinkUntilPlan {
		t.Fatalf("build until %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Build}, "plan_step"); g != agent.ThinkPlanStep {
		t.Fatalf("build plan_step %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortHigh}, ""); g != agent.ThinkOn {
		t.Fatalf("answer+high %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortLow}, ""); g != agent.ThinkOff {
		t.Fatalf("answer+low %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Lookup, Reasoning: false}, ""); g != agent.ThinkLookup {
		t.Fatalf("lookup %v", g)
	}
	if g := thinkingFor(intent.Decision{Intent: intent.Act, Reasoning: true}, ""); g != agent.ThinkPlanStep {
		t.Fatalf("act plan_step %v", g)
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
		if strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
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
	c := llm.New(srv.URL+"/v1", "main-model")
	var buf bytes.Buffer
	out, _, err := turn.StreamAnswer(context.Background(), c, []llm.Message{{Role: llm.RoleUser, Content: "q"}}, false, true, &buf, io.Discard, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" || buf.Len() != 0 {
		t.Fatalf("first %q buf %q", out, buf.String())
	}
	buf.Reset()
	out, _, err = turn.StreamAnswer(context.Background(), c, []llm.Message{{Role: llm.RoleUser, Content: "q"}}, false, true, &buf, io.Discard, 0, 0)
	if err != nil || out != "hi" || strings.TrimSpace(buf.String()) != "hi" {
		t.Fatalf("retry %q buf %q %v", out, buf.String(), err)
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
	prof := config.Profile{Name: "default", Memory: config.Memory{SessionIdleMinutes: 30}}
	cands := findCandidates(context.Background(), mem, prof, nil, cwd)
	if len(cands) != 1 || cands[0].Title != "quel process" || len(cands[0].Messages) != 1 || cands[0].Messages[0].Content != "port 3000" {
		t.Fatalf("%+v", cands)
	}
	got := applyResume(invocation{}, intent.Decision{ContinuesSession: 1}, cands)
	if got.ID != "" || len(got.Messages) != 1 || got.Messages[0].Content != "port 3000" {
		t.Fatalf("%+v", got)
	}
	got = applyResume(invocation{New: true}, intent.Decision{ContinuesSession: 1}, cands)
	if got.ID != "" || len(got.Messages) != 0 {
		t.Fatalf("new should ignore candidate: %+v", got)
	}
}

func TestTopicAwareResume(t *testing.T) {
	cands := []resumeCandidate{
		{
			ID:       "pdf",
			Title:    "read the current code and create docs/OVERVIEW.pdf explaining how it works with diagrams",
			LastUser: "read the current code and create docs/OVERVIEW.pdf explaining how it works with diagrams",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "create the pdf"}},
		},
		{
			ID:       "git",
			Title:    "how do I list git branches?",
			LastUser: "how do I list git branches?",
			Messages: []llm.Message{
				{Role: llm.RoleUser, Content: "how do I list git branches?"},
				{Role: llm.RoleAssistant, Content: "git branch"},
			},
		},
	}
	got := applyResume(invocation{Request: "and how do I delete one?"}, intent.Decision{ContinuesSession: 2, Intent: intent.Answer}, cands)
	if got.ID != "git" || got.Title != "how do I list git branches?" {
		t.Fatalf("git follow-up %+v", got)
	}
	got = applyResume(invocation{Request: "what is rust?"}, intent.Decision{ContinuesSession: 0, Intent: intent.Answer}, cands)
	if got.ID != "" || got.Title != "" {
		t.Fatalf("unrelated should start new: %+v", got)
	}
	got = applyResume(invocation{New: true}, intent.Decision{ContinuesSession: 2}, cands)
	if got.ID != "" {
		t.Fatalf("new flag: %+v", got)
	}
}

func TestModelErrorLine(t *testing.T) {
	ep := "http://gpu-box:8000/v1"
	if got := modelErrorLine(ep, llm.ErrIdleTimeout, false); !strings.Contains(got, "stopped responding (gpu-box:8000)") {
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
	if got := modelErrorLine(ep, dial, false); got != "konsolai: cannot reach the model server at gpu-box:8000. Run /doctor to check every endpoint." {
		t.Fatalf("dial %q", got)
	}
	if got := modelErrorLine(ep, llm.ErrIdleTimeout, true); !strings.Contains(got, llm.ErrIdleTimeout.Error()) {
		t.Fatalf("trace %q", got)
	}
}
