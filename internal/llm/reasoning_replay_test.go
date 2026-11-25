package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// reasoningServer emits a thinking delta followed by one tool call, then records
// the messages of the next request so a test can check the replay rule.
type reasoningServer struct {
	srv  *httptest.Server
	mu   sync.Mutex
	next []Message
	hits int
}

func newReasoningServer(t *testing.T) *reasoningServer {
	t.Helper()
	rs := &reasoningServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek", "max_model_len": 8192}}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rs.mu.Lock()
		rs.hits++
		n := rs.hits
		rs.next = body.Messages
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			writeReasonSSE(w, map[string]any{"choices": []map[string]any{{"delta": map[string]any{"reasoning_content": "I should read the file. "}}}})
			writeReasonSSE(w, map[string]any{"choices": []map[string]any{{"delta": map[string]any{"reasoning_content": "Then answer."}}}})
			writeReasonSSE(w, map[string]any{"choices": []map[string]any{{
				"delta": map[string]any{"tool_calls": []map[string]any{{
					"index": 0, "id": "call_1", "type": "function",
					"function": map[string]any{"name": "read_file", "arguments": `{"path":"a.txt"}`},
				}}},
				"finish_reason": "tool_calls",
			}}})
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		writeReasonSSE(w, map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "done"}}}})
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	rs.srv = httptest.NewServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

func writeReasonSSE(w http.ResponseWriter, payload map[string]any) {
	b, _ := json.Marshal(payload)
	_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
}

func (rs *reasoningServer) lastRequest() []Message {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]Message(nil), rs.next...)
}

func toolSpecForTest(name string) []ToolSpec {
	return []ToolSpec{{Type: "function", Function: FunctionSpec{
		Name: name, Parameters: json.RawMessage(`{"type":"object"}`),
	}}}
}

func TestReasoningContentIsCapturedAndReplayedWithTools(t *testing.T) {
	rs := newReasoningServer(t)
	c := New(rs.srv.URL+"/v1", "deepseek")
	c.PinContext(8192)
	tools := toolSpecForTest("read_file")

	msg, _, err := c.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "read a.txt"},
	}, tools, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "" {
		t.Fatalf("reasoning leaked into the answer content: %q", msg.Content)
	}
	if !strings.Contains(msg.ReasoningContent, "read the file") {
		t.Fatalf("reasoning not captured: %q", msg.ReasoningContent)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls %+v", msg.ToolCalls)
	}

	// The next request in the same tool turn must send the thinking text back.
	_, _, err = c.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "read a.txt"},
		msg,
		{Role: RoleTool, ToolCallID: "call_1", Content: "file body"},
	}, tools, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sent := rs.lastRequest()
	if len(sent) != 3 || sent[1].Role != RoleAssistant {
		t.Fatalf("request messages %+v", sent)
	}
	if !strings.Contains(sent[1].ReasoningContent, "read the file") {
		t.Fatalf("reasoning not replayed with tools: %q", sent[1].ReasoningContent)
	}
}

func TestReasoningContentIsNotSentWithoutTools(t *testing.T) {
	rs := newReasoningServer(t)
	c := New(rs.srv.URL+"/v1", "deepseek")
	c.PinContext(8192)
	msg, _, err := c.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "read a.txt"},
	}, toolSpecForTest("read_file"), ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "and now?"},
		msg,
	}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, m := range rs.lastRequest() {
		if m.ReasoningContent != "" {
			t.Fatalf("reasoning sent without tools: %q", m.ReasoningContent)
		}
	}
}

func TestJSONCompletionCapturesReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"answer","reasoning_content":"because"}}]}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "deepseek")
	c.PinContext(8192)
	msg, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "answer" || msg.ReasoningContent != "because" {
		t.Fatalf("message %+v", msg)
	}
}
