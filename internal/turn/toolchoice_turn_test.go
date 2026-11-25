package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
)

// strictChoiceServer is DeepSeek in thinking mode: any tool_choice other than
// the plain auto string is a 400.
type strictChoiceServer struct {
	srv     *httptest.Server
	mu      sync.Mutex
	replies []llm.Message
	n       int
	named   []string
}

func newStrictChoiceServer(t *testing.T, replies ...llm.Message) (*strictChoiceServer, *httptest.Server) {
	t.Helper()
	ss := &strictChoiceServer{replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-flash", "max_model_len": 8192}}})
			return
		}
		var body struct {
			ToolChoice json.RawMessage `json:"tool_choice"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		choice := strings.TrimSpace(string(body.ToolChoice))
		ss.mu.Lock()
		if choice != "" && choice != `"auto"` {
			ss.named = append(ss.named, choice)
			ss.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Thinking mode does not support this tool_choice"}}`)
			return
		}
		n := ss.n
		ss.n++
		ss.mu.Unlock()
		if n >= len(ss.replies) {
			http.Error(w, "no more replies", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": ss.replies[n]}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
		})
	}))
	ss.srv = srv
	t.Cleanup(srv.Close)
	return ss, srv
}

func toolCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}
}

func TestBuildTurnCompletesOnAutoMode(t *testing.T) {
	ss, srv := newStrictChoiceServer(t,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("1", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"],"commands":[]}`)}},
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("2", "finish", `{"summary":"done"}`)}},
	)
	prof := config.Builtin()
	prof.Models = map[string]config.Model{"deepseek-flash": {Endpoint: srv.URL + "/v1", Model: "deepseek-flash", ToolChoiceMode: "auto"}}
	prof.Roles.Main = "deepseek-flash"
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out, errb strings.Builder
	r := &Runner{
		Profile: prof, Mem: memory.NewGuard(nil, nil), Out: &out, Err: &errb,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return autoConfirm{}, func() {}, nil
		},
	}
	res := r.RunTurn(context.Background(), Request{Text: "change a.txt", Intent: "build", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errb.String())
	}
	ss.mu.Lock()
	named := append([]string(nil), ss.named...)
	ss.mu.Unlock()
	if len(named) != 0 {
		t.Fatalf("the strict server saw named tool_choice: %v", named)
	}
}
