package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
)

// strictActServer emulates a DeepSeek-style strict provider: it rejects a named
// or required tool_choice in thinking mode and separately rejects a request
// whose tool history is dangling. It also records every outcome so a retry can
// be counted exactly.
type strictActServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	replies  []llm.Message
	accepted int
	choices  []string
	named    int
	dangling int
	stall    bool
}

func newStrictActServer(t *testing.T, replies ...llm.Message) (*strictActServer, *httptest.Server) {
	return newStrictActServerMode(t, false, replies...)
}

func newStrictActServerMode(t *testing.T, stall bool, replies ...llm.Message) (*strictActServer, *httptest.Server) {
	t.Helper()
	st := &strictActServer{replies: replies, stall: stall}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-flash", "max_model_len": 8192}}})
			return
		}
		var body struct {
			ToolChoice json.RawMessage `json:"tool_choice"`
			Messages   []llm.Message   `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		choice := strings.TrimSpace(string(body.ToolChoice))
		badHistory := danglingToolHistory(body.Messages)
		st.mu.Lock()
		st.choices = append(st.choices, choice)
		if badHistory != "" {
			st.dangling++
		}
		if choice != "" && choice != `"auto"` {
			st.named++
		}
		accept := badHistory == "" && (choice == "" || choice == `"auto"`)
		n := st.accepted
		if accept {
			st.accepted++
		}
		// In stall mode, only the auto retry after a named rejection stalls:
		// the pre-plan step must reach the gate reject first.
		stall := st.stall && st.named > 0
		st.mu.Unlock()
		switch {
		case badHistory != "":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"invalid dangling tool_calls in the request history"}}`)
		case choice != "" && choice != `"auto"`:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Thinking mode does not support this tool_choice"}}`)
		case stall:
			// A fake that never answers the auto retry: the client may only
			// return through the caller's context, never through a deadlock.
			<-r.Context().Done()
		default:
			if n >= len(st.replies) {
				http.Error(w, "no more replies", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": st.replies[n]}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
			})
		}
	}))
	st.srv = srv
	t.Cleanup(srv.Close)
	return st, srv
}

// danglingToolHistory returns a description of the first tool-history defect:
// an assistant tool call without its one reply per id, a reply out of order, or
// an orphan tool reply. RepairToolCalls should prevent all three from leaving
// the client.
func danglingToolHistory(msgs []llm.Message) string {
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == llm.RoleTool {
			return fmt.Sprintf("orphan tool reply %q at %d", m.ToolCallID, i)
		}
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		next := i + 1
		for _, call := range m.ToolCalls {
			if next >= len(msgs) {
				return fmt.Sprintf("missing reply for %q", call.ID)
			}
			reply := msgs[next]
			if reply.Role != llm.RoleTool || reply.ToolCallID != call.ID {
				return fmt.Sprintf("reply for %q is %s/%q", call.ID, reply.Role, reply.ToolCallID)
			}
			next++
		}
		i = next - 1
	}
	return ""
}

func (st *strictActServer) snapshot() (choices []string, named, dangling, accepted int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.choices...), st.named, st.dangling, st.accepted
}

func strictActRunner(t *testing.T, endpoint string) *Runner {
	t.Helper()
	prof := config.Builtin()
	prof.Models = map[string]config.Model{
		"deepseek-flash": {Endpoint: endpoint, Model: "deepseek-flash", MaxTokensField: "max_tokens"},
	}
	prof.Roles.Main = "deepseek-flash"
	prof.LLM.ToolChoiceMode = "named"
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &Runner{
		Profile: prof, Mem: memory.NewGuard(nil, nil), Out: io.Discard, Err: io.Discard,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return autoConfirm{}, func() {}, nil
		},
	}
}

// strictActScript drives the P0-A flow: a pre-plan mutating call so the gate
// reject forces a named propose_plan choice, then one plan, then finish.
func strictActScript() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("1", "write_file", `{"path":"a.txt","content":"a"}`)}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("2", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"],"commands":[]}`)}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("3", "finish", `{"summary":"done"}`)}},
	}
}

// runStrictTurn runs one act turn with a required plan under a bounded context.
// The result channel + context select means a product that blocks instead of
// returning fails the test instead of hanging the run.
func runStrictTurn(t *testing.T, r *Runner, budget time.Duration) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	type outcome struct {
		res Result
	}
	done := make(chan outcome, 1)
	go func() {
		res := r.RunTurn(ctx, Request{
			Text: "create a.txt and finish", Intent: "act", Effort: "high",
			Yes: true, Cwd: t.TempDir(), New: true,
		})
		done <- outcome{res: res}
	}()
	select {
	case got := <-done:
		return got.res
	case <-ctx.Done():
	}
	// The deadline fired: the product may return as it unwinds. Give it a
	// short grace window; a real hang never returns and fails the test.
	grace := time.NewTimer(5 * time.Second)
	defer grace.Stop()
	select {
	case got := <-done:
		return got.res
	case <-grace.C:
		t.Fatalf("product hang: RunTurn did not return after its context ended (%s)", budget)
		return Result{}
	}
}

// TestStrictToolChoiceActPlanDoesNotHang is the non-PTY reproduction of the
// P0-A flow: act with a required plan, a gate reject forcing a named
// tool_choice, a strict 400, one auto retry, then the plan and finish.
func TestStrictToolChoiceActPlanDoesNotHang(t *testing.T) {
	st, srv := newStrictActServer(t, strictActScript()...)
	r := strictActRunner(t, srv.URL+"/v1")
	res := runStrictTurn(t, r, 10*time.Second)
	if res.ExitCode != 0 {
		choices, named, dangling, accepted := st.snapshot()
		t.Fatalf("exit %d: choices=%v named=%d dangling=%d accepted=%d", res.ExitCode, choices, named, dangling, accepted)
	}
	choices, named, dangling, accepted := st.snapshot()
	if named != 1 {
		t.Fatalf("want exactly one named tool_choice attempt, got %d: %v", named, choices)
	}
	if dangling != 0 {
		t.Fatalf("the client sent a dangling tool history: %v", choices)
	}
	if accepted != len(strictActScript()) {
		t.Fatalf("accepted requests %d, want %d: %v", accepted, len(strictActScript()), choices)
	}
	// A silent infinite auto retry would show up as repeated identical requests.
	if len(choices) > 6 {
		t.Fatalf("too many attempts for one rejection: %v", choices)
	}
}

// TestStrictDanglingHistoryIsRepaired proves the second P0-A ingredient: a
// history with an unanswered assistant tool call is repaired before the
// request reaches the strict provider, so the provider never rejects it.
func TestStrictDanglingHistoryIsRepaired(t *testing.T) {
	st, srv := newStrictActServer(t, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	client := llm.New(srv.URL+"/v1", "deepseek-flash")
	client.PinContext(8192)
	// Assistant tool call with an orphaned tool reply and a missing reply:
	// RepairToolCalls must rebuild the group before the strict fake sees it.
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("a", "read_file", `{"path":"x"}`)}},
		{Role: llm.RoleTool, ToolCallID: "other", Content: "orphan"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := client.Complete(ctx, msgs, nil, llm.ChatOptions{}); err != nil {
		t.Fatalf("the repaired history was rejected: %v", err)
	}
	_, named, dangling, accepted := st.snapshot()
	if dangling != 0 {
		t.Fatalf("the strict provider saw dangling tool history")
	}
	if named != 0 {
		t.Fatalf("the unrelated call sent a tool_choice: named=%d", named)
	}
	if accepted != 1 {
		t.Fatalf("accepted %d requests", accepted)
	}
}

// TestStrictStalledRetryHonorsTurnContext proves the other half of the P0-A
// duration: when the provider accepts the auto retry and never answers, the
// turn returns on the caller's context instead of waiting for the 120s stream
// idle timeout. The 1s budget bounds the test, not the product.
func TestStrictStalledRetryHonorsTurnContext(t *testing.T) {
	st, srv := newStrictActServerMode(t, true, strictActScript()...)
	r := strictActRunner(t, srv.URL+"/v1")
	start := time.Now()
	res := runStrictTurn(t, r, time.Second)
	elapsed := time.Since(start)
	if res.ExitCode == 0 {
		t.Fatalf("a stalled retry must not exit 0")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the stalled retry waited %s", elapsed)
	}
	_, named, dangling, _ := st.snapshot()
	if named != 1 {
		t.Fatalf("the named attempt did not occur: named=%d dangling=%d", named, dangling)
	}
}
