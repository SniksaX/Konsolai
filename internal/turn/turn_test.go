package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/ui"
)

type autoConfirm struct{}

func (autoConfirm) Confirm(string, string) (ui.Decision, string, error) { return ui.Approve, "", nil }
func (autoConfirm) ConfirmForced(string, string) (ui.Decision, string, error) {
	return ui.Approve, "", nil
}

type quitConfirm struct{}

func (quitConfirm) Confirm(string, string) (ui.Decision, string, error) { return ui.Quit, "", nil }
func (quitConfirm) ConfirmForced(string, string) (ui.Decision, string, error) {
	return ui.Quit, "", nil
}

func fakeLLM(t *testing.T, replies []llm.Message) *llm.Client {
	t.Helper()
	var i int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if i >= len(replies) {
			http.Error(w, "no more", 500)
			return
		}
		msg := replies[i]
		i++
		if len(msg.ToolCalls) == 0 && msg.Content != "" {
			w.Header().Set("Content-Type", "text/event-stream")
			payload, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": msg.Content}}},
			})
			_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": msg}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return llm.New(srv.URL+"/v1", "test")
}

func testRunner(t *testing.T, client *llm.Client, conf agent.Confirmer) *Runner {
	t.Helper()
	if os.Getenv("XDG_STATE_HOME") == "" {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
	}
	return &Runner{
		Profile: config.Profile{Name: "default", SessionIdleMinutes: 30, BuildThinking: "plan_step"},
		Client:  client,
		Mem:     memory.NewGuard(nil, nil),
		Out:     io.Discard,
		Err:     io.Discard,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return conf, func() {}, nil
		},
	}
}

func TestRunTurnAnswer(t *testing.T) {
	client := fakeLLM(t, []llm.Message{{Role: llm.RoleAssistant, Content: "hello there"}})
	r := testRunner(t, client, autoConfirm{})
	var errB strings.Builder
	r.Err = &errB
	res := r.RunTurn(context.Background(), Request{Text: "hi", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d err=%s", res.ExitCode, errB.String())
	}
	if res.Intent != "answer" {
		t.Fatalf("intent %s", res.Intent)
	}
	if !strings.Contains(res.Answer, "hello") {
		t.Fatalf("answer %q", res.Answer)
	}
}

func TestRunTurnAgentFinish(t *testing.T) {
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"looked up"}`},
		}}},
	})
	r := testRunner(t, client, autoConfirm{})
	res := r.RunTurn(context.Background(), Request{Text: "what is here", Intent: "lookup", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if res.Intent != "lookup" {
		t.Fatalf("intent %s", res.Intent)
	}
}

func TestRunTurnResumeLocal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	if err := memory.SaveLast(memory.LastSession{
		Profile:  "default",
		CWD:      cwd,
		Title:    "earlier",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "first"}, {Role: llm.RoleAssistant, Content: "ok"}},
	}); err != nil {
		t.Fatal(err)
	}
	client := fakeLLM(t, []llm.Message{{Role: llm.RoleAssistant, Content: "again"}})
	r := testRunner(t, client, autoConfirm{})
	var out strings.Builder
	r.Out = &out
	res := r.RunTurn(context.Background(), Request{Text: "and then?", Intent: "answer", Yes: true, Cwd: cwd})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if !strings.Contains(out.String(), "continuing session") {
		t.Fatalf("want resume line, got %q", out.String())
	}
}

func TestRunTurnResolvesWindow(t *testing.T) {
	client := fakeLLM(t, []llm.Message{{Role: llm.RoleAssistant, Content: "ok"}})
	if client.ContextWindow() != 32768 {
		t.Fatalf("pre-turn window %d", client.ContextWindow())
	}
	r := testRunner(t, client, autoConfirm{})
	res := r.RunTurn(context.Background(), Request{Text: "hi", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if client.ContextWindow() != 8192 {
		t.Fatalf("want max_model_len 8192, got %d", client.ContextWindow())
	}
}

func TestThinkingForEffort(t *testing.T) {
	if g := ThinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortNone}, "", ThinkAuto); g != agent.ThinkOff {
		t.Fatalf("none %v", g)
	}
	if g := ThinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortLow}, "", ThinkAuto); g != agent.ThinkOff {
		t.Fatalf("low %v", g)
	}
	if g := ThinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortHigh}, "", ThinkAuto); g != agent.ThinkOn {
		t.Fatalf("high %v", g)
	}
	if g := ThinkingFor(intent.Decision{Intent: intent.Lookup}, "plan_step", ThinkAuto); g != agent.ThinkLookup {
		t.Fatalf("lookup %v", g)
	}
	if g := ThinkingFor(intent.Decision{Intent: intent.Act}, "plan_step", ThinkAuto); g != agent.ThinkPlanStep {
		t.Fatalf("act %v", g)
	}
	if g := ThinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortNone}, "", ThinkAlways); g != agent.ThinkOn {
		t.Fatalf("override on %v", g)
	}
	if g := ThinkingFor(intent.Decision{Intent: intent.Answer, Effort: intent.EffortHigh}, "", ThinkNever); g != agent.ThinkOff {
		t.Fatalf("override off %v", g)
	}
}

func TestStripThinkPrefix(t *testing.T) {
	got, ok := StripThinkPrefix("think hard why is this slow")
	if !ok || got != "why is this slow" {
		t.Fatalf("%q %v", got, ok)
	}
	got, ok = StripThinkPrefix("Réfléchis bien: fix the bug")
	if !ok || got != "fix the bug" {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := StripThinkPrefix("hello"); ok {
		t.Fatal("no prefix")
	}
}

func TestFastUnreachableFallsBack(t *testing.T) {
	llm.ResetReachCache()
	t.Cleanup(llm.ResetReachCache)
	client := fakeLLM(t, []llm.Message{{Role: llm.RoleAssistant, Content: `{"intent":"answer","language":"en","reason":"hi","reasoning":false,"effort":"none","task":"other","continues_session":0}`}, {Role: llm.RoleAssistant, Content: "hello"}})
	r := testRunner(t, client, autoConfirm{})
	r.Profile.FastEndpoint = "http://127.0.0.1:1/v1"
	r.FastClient = llm.NewFast(r.Profile.FastEndpoint, "mini")
	var errB strings.Builder
	r.Err = &errB
	res := r.RunTurn(context.Background(), Request{Text: "hi", Trace: true, Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, errB.String())
	}
	if r.RouterSource != "main" {
		t.Fatalf("source %s", r.RouterSource)
	}
	if !strings.Contains(errB.String(), "router") || !strings.Contains(errB.String(), "effort") {
		t.Fatalf("trace %q", errB.String())
	}
}

func TestRunTurnAbortExit(t *testing.T) {
	plan := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
		ID: "1", Type: "function", Function: llm.FunctionCall{Name: "propose_plan", Arguments: `{"summary":"x","steps":["y"],"files":["a.txt"]}`},
	}}}
	client := fakeLLM(t, []llm.Message{plan, plan})
	r := testRunner(t, client, quitConfirm{})
	res := r.RunTurn(context.Background(), Request{Text: "write a", Intent: "act", Yes: false, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 3 {
		t.Fatalf("exit %d", res.ExitCode)
	}
}

func TestShellOutputRing(t *testing.T) {
	var r Runner
	if _, ok := r.ShellOutput(0); ok {
		t.Fatal("empty")
	}
	r.RememberShell("one")
	r.RememberShell("two")
	r.RememberShell("three")
	got, ok := r.ShellOutput(0)
	if !ok || got != "three" {
		t.Fatalf("last %q %v", got, ok)
	}
	got, ok = r.ShellOutput(1)
	if !ok || got != "two" {
		t.Fatalf("back1 %q %v", got, ok)
	}
	if _, ok := r.ShellOutput(3); ok {
		t.Fatal("overflow")
	}
	for i := 0; i < 8; i++ {
		r.RememberShell(strings.Repeat("x", 10) + string(rune('a'+i)))
	}
	if len(r.shellOut) != 5 {
		t.Fatalf("kept %d", len(r.shellOut))
	}
	big := strings.Repeat("z", maxShellBytes+50)
	r.RememberShell(big)
	got, _ = r.ShellOutput(0)
	if len(got) != maxShellBytes {
		t.Fatalf("clip %d", len(got))
	}
}
