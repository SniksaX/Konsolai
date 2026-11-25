package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// deepSeekListError validates a chat message list the way DeepSeek does: every
// assistant message with tool_calls must be followed by exactly one tool
// message per tool_call_id, in the same order, before any other message.
func deepSeekListError(msgs []llm.Message) error {
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == llm.RoleTool {
			return fmt.Errorf("message %d is a tool reply with tool_call_id=%q but no assistant tool_calls owns it", i, m.ToolCallID)
		}
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		ids := make([]string, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			ids[j] = tc.ID
		}
		got := 0
		for j := i + 1; j < len(msgs) && msgs[j].Role == llm.RoleTool; j++ {
			if got >= len(ids) {
				return fmt.Errorf("assistant at %d owns %d tool_calls but a reply for %q follows", i, len(ids), msgs[j].ToolCallID)
			}
			if msgs[j].ToolCallID != ids[got] {
				return fmt.Errorf("assistant at %d: tool reply %d is %q, want %q", i, got, msgs[j].ToolCallID, ids[got])
			}
			got++
		}
		if got != len(ids) {
			return fmt.Errorf("assistant at %d has tool_calls %v but only %d tool replies", i, ids, got)
		}
		i += got
	}
	return nil
}

// describeMessages prints only roles and tool ids, never content.
func describeMessages(msgs []llm.Message) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		s := string(m.Role)
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			ids := make([]string, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				ids[i] = tc.ID
			}
			s += " tool_calls=[" + strings.Join(ids, ",") + "]"
		}
		if m.Role == llm.RoleTool {
			s += " tool_call_id=" + m.ToolCallID
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ; ")
}

type fakeDeepSeek struct {
	srv *httptest.Server
	mu  sync.Mutex
	// reply returns the next chat body and whether it is SSE.
	reply func(n int, msgs []llm.Message) (string, bool)
	// inspect, when set, sees every accepted request before the reply.
	inspect    func(msgs []llm.Message, hasTools bool)
	hits       int
	violations []string
	lists      []string
}

func newFakeDeepSeek(t *testing.T, reply func(n int, msgs []llm.Message) (string, bool)) *fakeDeepSeek {
	t.Helper()
	fd := &fakeDeepSeek{reply: reply}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "fake-model", "max_model_len": 8192}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []llm.Message     `json:"messages"`
			Tools    []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fd.mu.Lock()
		fd.hits++
		n := fd.hits
		list := describeMessages(req.Messages)
		fd.lists = append(fd.lists, list)
		verr := deepSeekListError(req.Messages)
		if verr != nil {
			fd.violations = append(fd.violations, verr.Error())
			fd.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"message": "invalid message list: " + verr.Error(),
			}})
			return
		}
		fd.mu.Unlock()
		if fd.inspect != nil {
			fd.inspect(req.Messages, len(req.Tools) > 0)
		}
		body, sse := fd.replyFor(n, req.Messages)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, body)
	})
	fd.srv = httptest.NewServer(mux)
	t.Cleanup(fd.srv.Close)
	return fd
}

func (fd *fakeDeepSeek) replyFor(n int, msgs []llm.Message) (string, bool) {
	return fd.reply(n, msgs)
}

func (fd *fakeDeepSeek) evidence() string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	var b strings.Builder
	for i, list := range fd.lists {
		fmt.Fprintf(&b, "request %d: %s\n", i+1, list)
	}
	for _, v := range fd.violations {
		fmt.Fprintf(&b, "violation: %s\n", v)
	}
	return b.String()
}

func jsonAnswer(content string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
	})
	return string(b)
}

func jsonToolCalls(calls ...llm.ToolCall) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{
			"role": "assistant", "content": "", "tool_calls": calls,
		}}},
		"usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
	})
	return string(b)
}

func sseAnswer(content string) string {
	delta, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{"content": content}}},
	})
	usage, _ := json.Marshal(map[string]any{
		"choices": []any{},
		"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
	})
	return "data: " + string(delta) + "\n\ndata: " + string(usage) + "\n\ndata: [DONE]\n\n"
}

func fakeCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}
}

// approveThenQuit approves the first confirmation and quits at the second, so
// a parallel tool-call step is interrupted with unanswered calls.
type approveThenQuit struct{ calls int }

func (a *approveThenQuit) Confirm(string, string) (ui.Decision, string, error) {
	a.calls++
	if a.calls == 1 {
		return ui.Approve, "", nil
	}
	return ui.Quit, "", nil
}

func (a *approveThenQuit) ConfirmForced(summary, preview string) (ui.Decision, string, error) {
	return a.Confirm(summary, preview)
}

// historyRunner builds a store-backed Runner whose main role is the fake server.
func historyRunner(t *testing.T, fd *fakeDeepSeek, mem *memory.Guard, conf agent.Confirmer) *Runner {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	prof := config.Builtin()
	prof.Models = map[string]config.Model{"fake": {Endpoint: fd.srv.URL + "/v1", Model: "fake-model"}}
	prof.Roles.Main = "fake"
	var out, errb strings.Builder
	return &Runner{
		Profile: prof, Mem: mem, Out: &out, Err: &errb,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) { return conf, func() {}, nil },
	}
}

func TestResumeAfterInterruptedParallelCallsSendsValidHistory(t *testing.T) {
	st, _ := openLeakDB(t)
	fd := newFakeDeepSeek(t, func(n int, _ []llm.Message) (string, bool) {
		if n == 1 {
			return jsonToolCalls(
				fakeCall("call_1", "write_file", `{"path":"a.txt","content":"a"}`),
				fakeCall("call_2", "write_file", `{"path":"b.txt","content":"b"}`),
			), false
		}
		return jsonAnswer("done"), false
	})
	mem := memory.NewGuard(st, func(error) {})
	conf := &approveThenQuit{}
	r := historyRunner(t, fd, mem, conf)
	cwd := t.TempDir()
	first := r.RunTurn(context.Background(), Request{Text: "write both files", Intent: "act", Yes: true, Cwd: cwd, New: true})
	if first.ExitCode != 3 {
		t.Fatalf("first exit %d: %s", first.ExitCode, r.Err)
	}
	if first.SessionID == "" {
		t.Fatal("no session")
	}
	rows, err := st.LoadMessages(context.Background(), first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	stored := memory.ToLLM(rows)
	if verr := deepSeekListError(stored); verr != nil {
		t.Fatalf("the interrupted turn stored a dangling tool_calls group (%s):\n%s", verr, describeMessages(stored))
	}
	if !strings.Contains(describeMessages(stored), "tool tool_call_id=call_2") {
		t.Fatalf("no synthetic reply recorded for the unanswered call:\n%s", describeMessages(stored))
	}
	second := r.RunTurn(context.Background(), Request{Text: "continue", Intent: "act", Yes: true, Cwd: cwd, ResumeSession: first.SessionID})
	if len(fd.violations) > 0 {
		t.Fatalf("the resumed turn sent a dangling tool_calls list:\n%s", fd.evidence())
	}
	if second.ExitCode != 0 {
		t.Fatalf("second exit %d: %s", second.ExitCode, r.Err)
	}
}

// appendRows writes a raw message sequence into a session, the way an earlier
// interrupted run would have left it.
func appendRows(t *testing.T, st *store.Store, sessionID string, msgs []store.Message) {
	t.Helper()
	ctx := context.Background()
	for i, m := range msgs {
		if err := st.AppendMessage(ctx, sessionID, i+1, m); err != nil {
			t.Fatal(err)
		}
	}
}

func toolCallJSON(t *testing.T, calls ...llm.ToolCall) []byte {
	t.Helper()
	b, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnswerResumeDropsUnansweredToolCalls(t *testing.T) {
	st, _ := openLeakDB(t)
	fd := newFakeDeepSeek(t, func(int, []llm.Message) (string, bool) {
		return jsonAnswer("because"), false
	})
	mem := memory.NewGuard(st, func(error) {})
	r := historyRunner(t, fd, mem, &approveThenQuit{})
	cwd := t.TempDir()
	ctx := context.Background()
	sid, err := st.CreateSession(ctx, store.NewSession{Profile: r.Profile.Name, CWD: cwd, Mode: "answer", Model: "fake-model", Title: "tool history"})
	if err != nil {
		t.Fatal(err)
	}
	appendRows(t, st, sid, []store.Message{
		{Role: "user", Content: "write it"},
		{Role: "assistant", Content: "Writing now.", ToolCalls: toolCallJSON(t, fakeCall("call_1", "write_file", `{"path":"a.txt","content":"a"}`))},
		{Role: "tool", Content: "wrote a.txt", ToolCallID: "call_1"},
	})
	res := r.RunTurn(ctx, Request{Text: "why did that fail?", Intent: "answer", Effort: "low", Yes: true, Cwd: cwd, ResumeSession: sid})
	if len(fd.violations) > 0 {
		t.Fatalf("the answer turn sent assistant tool_calls without replies:\n%s", fd.evidence())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, r.Err)
	}
	if list := strings.Join(fd.lists, "\n"); strings.Contains(list, "tool_calls") {
		t.Fatalf("the answer history still carries tool_calls:\n%s", fd.evidence())
	}
}

func TestTruncatedRetryKeepsToolPairs(t *testing.T) {
	st, _ := openLeakDB(t)
	fd := newFakeDeepSeek(t, func(n int, _ []llm.Message) (string, bool) {
		if n == 1 {
			truncated, _ := json.Marshal(map[string]any{"choices": []map[string]any{{
				"delta": map[string]any{"content": "cut"}, "finish_reason": "length",
			}}})
			return "data: " + string(truncated) + "\n\ndata: [DONE]\n\n", true
		}
		return jsonAnswer("done"), false
	})
	mem := memory.NewGuard(st, func(error) {})
	r := historyRunner(t, fd, mem, autoConfirm{})
	cwd := t.TempDir()
	ctx := context.Background()
	sid, err := st.CreateSession(ctx, store.NewSession{Profile: r.Profile.Name, CWD: cwd, Mode: "act", Model: "fake-model", Title: "long history"})
	if err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{
		fakeCall("call_1", "write_file", `{"path":"a.txt","content":"a"}`),
		fakeCall("call_2", "write_file", `{"path":"b.txt","content":"b"}`),
		fakeCall("call_3", "write_file", `{"path":"c.txt","content":"c"}`),
	}
	appendRows(t, st, sid, []store.Message{
		{Role: "user", Content: "write three files"},
		{Role: "assistant", ToolCalls: toolCallJSON(t, calls...)},
		{Role: "tool", Content: "wrote a", ToolCallID: "call_1"},
		{Role: "tool", Content: "wrote b", ToolCallID: "call_2"},
		{Role: "tool", Content: "wrote c", ToolCallID: "call_3"},
	})
	res := r.RunTurn(ctx, Request{Text: "continue", Intent: "act", Yes: true, Cwd: cwd, ResumeSession: sid})
	if len(fd.violations) > 0 {
		t.Fatalf("the truncated retry sent an invalid list:\n%s", fd.evidence())
	}
	lists := strings.Join(fd.lists, "\n")
	if !strings.Contains(lists, "assistant tool_calls=[call_1,call_2,call_3]") {
		t.Fatalf("the emergency trim dropped the tool_calls pair:\n%s", fd.evidence())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, r.Err)
	}
}

func TestCompactBoundaryDoesNotSplitToolPairs(t *testing.T) {
	st, _ := openLeakDB(t)
	fd := newFakeDeepSeek(t, func(int, []llm.Message) (string, bool) {
		return jsonAnswer("done"), false
	})
	mem := memory.NewGuard(st, func(error) {})
	r := historyRunner(t, fd, mem, autoConfirm{})
	cwd := t.TempDir()
	ctx := context.Background()
	sid, err := st.CreateSession(ctx, store.NewSession{Profile: r.Profile.Name, CWD: cwd, Mode: "act", Model: "fake-model", Title: "compacted"})
	if err != nil {
		t.Fatal(err)
	}
	calls := []llm.ToolCall{
		fakeCall("call_1", "write_file", `{"path":"a.txt","content":"a"}`),
		fakeCall("call_2", "write_file", `{"path":"b.txt","content":"b"}`),
	}
	appendRows(t, st, sid, []store.Message{
		{Role: "user", Content: "write two files"},
		{Role: "assistant", ToolCalls: toolCallJSON(t, calls...)},
		{Role: "tool", Content: "wrote a", ToolCallID: "call_1"},
		{Role: "tool", Content: "wrote b", ToolCallID: "call_2"},
	})
	// A compact boundary that falls between the assistant and its replies must
	// not leak a dangling tool_calls group into the resumed context.
	if err := st.SetCompact(ctx, sid, 1, "earlier: wrote two files"); err != nil {
		t.Fatal(err)
	}
	res := r.RunTurn(ctx, Request{Text: "continue", Intent: "act", Yes: true, Cwd: cwd, ResumeSession: sid})
	if len(fd.violations) > 0 {
		t.Fatalf("the compacted history sent an invalid list:\n%s", fd.evidence())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, r.Err)
	}
}

func TestElisionKeepsToolPairsValid(t *testing.T) {
	fd := newFakeDeepSeek(t, func(n int, _ []llm.Message) (string, bool) {
		if n == 1 {
			return jsonToolCalls(fakeCall("call_1", "run_shell", `{"cmd":"seq 1 60000"}`)), false
		}
		return jsonAnswer("done"), false
	})
	mem := memory.NewGuard(nil, func(error) {})
	r := historyRunner(t, fd, mem, autoConfirm{})
	res := r.RunTurn(context.Background(), Request{Text: "count", Intent: "act", Yes: true, Cwd: t.TempDir(), New: true})
	if len(fd.violations) > 0 {
		t.Fatalf("the elided context sent an invalid list:\n%s", fd.evidence())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, r.Err)
	}
}

// TestFinishedStepRecordsACompletePair: a finish tool call ends the turn before
// its reply is appended, so the recorded history must still get one.
func TestFinishedStepRecordsACompletePair(t *testing.T) {
	st, _ := openLeakDB(t)
	fd := newFakeDeepSeek(t, func(n int, _ []llm.Message) (string, bool) {
		if n == 1 {
			return jsonToolCalls(fakeCall("call_1", "finish", `{"summary":"done"}`)), false
		}
		return jsonAnswer("ok"), false
	})
	mem := memory.NewGuard(st, func(error) {})
	r := historyRunner(t, fd, mem, autoConfirm{})
	cwd := t.TempDir()
	ctx := context.Background()
	first := r.RunTurn(ctx, Request{Text: "do it", Intent: "act", Yes: true, Cwd: cwd, New: true})
	if first.ExitCode != 0 {
		t.Fatalf("first exit %d: %s", first.ExitCode, r.Err)
	}
	rows, err := st.LoadMessages(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	stored := memory.ToLLM(rows)
	if verr := deepSeekListError(stored); verr != nil {
		t.Fatalf("the finished step stored a dangling tool_calls group (%s):\n%s", verr, describeMessages(stored))
	}
	second := r.RunTurn(ctx, Request{Text: "continue", Intent: "act", Yes: true, Cwd: cwd, ResumeSession: first.SessionID})
	if len(fd.violations) > 0 {
		t.Fatalf("the resumed turn sent an invalid list:\n%s", fd.evidence())
	}
	if second.ExitCode != 0 {
		t.Fatalf("second exit %d: %s", second.ExitCode, r.Err)
	}
}
