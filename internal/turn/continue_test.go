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
	"github.com/siraj/konsolai/internal/llm"
)

func TestContinueReusesActAfterAbort(t *testing.T) {
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{Name: "write_file", Arguments: `{"path":"a.txt","content":"x"}`},
		}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "2", Type: "function", Function: llm.FunctionCall{Name: "write_file", Arguments: `{"path":"b.txt","content":"ok"}`},
		}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "3", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"wrote"}`},
		}}},
	})
	r := testRunner(t, client, quitConfirm{})
	cwd := t.TempDir()
	res := r.RunTurn(context.Background(), Request{Text: "write a.txt", Intent: "act", Cwd: cwd, New: true})
	if res.ExitCode != 3 {
		t.Fatalf("abort %d", res.ExitCode)
	}
	r.OpenConfirm = func(bool) (agent.Confirmer, func(), error) {
		return autoConfirm{}, func() {}, nil
	}
	res = r.RunTurn(context.Background(), Request{Text: "continue", Yes: true, Cwd: cwd})
	if res.Intent != "act" {
		t.Fatalf("intent %s", res.Intent)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	b, err := os.ReadFile(cwd + "/b.txt")
	if err != nil || string(b) != "ok" {
		t.Fatalf("continue did not finish: %q %v", b, err)
	}
}

func TestFastJinjaRetriesMain(t *testing.T) {
	llm.ResetReachCache()
	t.Cleanup(llm.ResetReachCache)
	jinja := `{"error":"jinja2.exceptions.TemplateError: expected user role"}`
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "mini", "meta": map[string]any{"n_ctx": 8192}}},
			})
			return
		}
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "Classify the user's request") {
			w.Header().Set("Content-Type", "text/event-stream")
			payload, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": `{"intent":"answer","language":"en","reason":"hi","reasoning":false,"effort":"none","task":"other","continues_session":0}`}}},
			})
			_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		http.Error(w, jinja, 500)
	}))
	t.Cleanup(fast.Close)
	client := fakeLLM(t, []llm.Message{{Role: llm.RoleAssistant, Content: "from-main"}})
	r := testRunner(t, client, autoConfirm{})
	r.FastClient = llm.NewFast(fast.URL+"/v1", "mini")
	var errB strings.Builder
	r.Err = &errB
	res := r.RunTurn(context.Background(), Request{Text: "hi", Trace: true, Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, errB.String())
	}
	if !strings.Contains(res.Answer, "from-main") {
		t.Fatalf("answer %q", res.Answer)
	}
	if !strings.Contains(errB.String(), "fast model failed") {
		t.Fatalf("trace %q", errB.String())
	}
	if strings.Contains(errB.String(), "jinja") || strings.Contains(errB.String(), `"error"`) {
		t.Fatalf("raw json leaked %q", errB.String())
	}
}

func TestModelErrorLineHidesJSON(t *testing.T) {
	err := &llm.HTTPError{Status: 500, Body: `{"error":"jinja boom"}`}
	s := ModelErrorLine("http://gpu-box:8000/v1", err, false)
	if strings.Contains(s, "{") || strings.Contains(s, "jinja") {
		t.Fatalf("%q", s)
	}
}
