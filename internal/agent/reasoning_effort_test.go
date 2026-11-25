package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestAgentTraceNamesTheReasoningEffortFallback(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6-luna","max_model_len":128000}]}`))
			return
		}
		var body struct {
			Tools           []json.RawMessage `json:"tools"`
			ReasoningEffort string            `json:"reasoning_effort"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		hits++
		if len(body.Tools) > 0 && body.ReasoningEffort != "none" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Function tools with reasoning_effort are not supported. Set reasoning_effort to 'none'."}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role": "assistant", "tool_calls": []map[string]any{{
					"id": "1", "type": "function",
					"function": map[string]any{"name": "finish", "arguments": `{"summary":"done"}`},
				}},
			}}},
			"usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 2},
		})
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "gpt-6-luna")
	c.PinContext(128000)
	c.SetRetry(-1, 0)
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var errb strings.Builder
	trace := true
	if err := Run(context.Background(), Config{
		Client: c, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, Err: &errb,
		MaxSteps: 4, Trace: trace, Intent: "act",
	}, "sys", "do it"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if hits != 2 {
		t.Fatalf("want one retry, hits %d", hits)
	}
	if !strings.Contains(errb.String(), "reasoning_effort not supported with tools; using none for this model (retried once)") {
		t.Fatalf("trace does not name the fallback: %q", errb.String())
	}
}
