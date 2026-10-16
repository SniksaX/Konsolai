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

func TestRunTurnAbortExit(t *testing.T) {
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{Name: "propose_plan", Arguments: `{"summary":"x","steps":["y"],"files":["a.txt"]}`},
		}}},
	})
	r := testRunner(t, client, quitConfirm{})
	res := r.RunTurn(context.Background(), Request{Text: "write a", Intent: "act", Yes: false, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 3 {
		t.Fatalf("exit %d", res.ExitCode)
	}
}
