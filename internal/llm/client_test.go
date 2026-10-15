package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCompleteParsesToolCallsAndSendsTools(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(completeResponse{
			Choices: []struct {
				Message Message `json:"message"`
			}{{Message: Message{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: FunctionCall{
						Name:      "run_shell",
						Arguments: `{"cmd":"true"}`,
					},
				}},
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	specs := []ToolSpec{{
		Type: "function",
		Function: FunctionSpec{
			Name:        "run_shell",
			Description: "run",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
	}}
	msg, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, specs, ChatOptions{Thinking: true, Temperature: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "run_shell" {
		t.Fatalf("tool calls: %+v", msg.ToolCalls)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tools"]; !ok {
		t.Fatal("missing tools")
	}
	var choice string
	if err := json.Unmarshal(raw["tool_choice"], &choice); err != nil || choice != "auto" {
		t.Fatalf("tool_choice %s %v", raw["tool_choice"], err)
	}
	var kwargs chatTemplateKwargs
	if err := json.Unmarshal(raw["chat_template_kwargs"], &kwargs); err != nil {
		t.Fatal(err)
	}
	if !kwargs.EnableThinking {
		t.Fatal("enable_thinking want true")
	}
}

func TestCompleteOmitsToolsWhenEmpty(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(completeResponse{
			Choices: []struct {
				Message Message `json:"message"`
			}{{Message: Message{Role: RoleAssistant, Content: "ok"}}},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	_, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tools"]; ok {
		t.Fatal("tools should be omitted")
	}
	if _, ok := raw["tool_choice"]; ok {
		t.Fatal("tool_choice should be omitted")
	}
}

func TestCompleteHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	_, err := c.Complete(context.Background(), nil, nil, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("got %v", err)
	}
}

func TestCompleteErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(completeResponse{Error: &struct {
			Message string `json:"message"`
		}{Message: "bad model"}})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	_, err := c.Complete(context.Background(), nil, nil, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("got %v", err)
	}
}

func TestCompleteEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(completeResponse{})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	_, err := c.Complete(context.Background(), nil, nil, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("got %v", err)
	}
}

func TestStreamDeltasAndDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		fl.Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fl.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	var got string
	err := c.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, ChatOptions{}, func(d string) error {
		got += d
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamErrorChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"boom\"}}\n\n")
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "qwen")
	err := c.Stream(context.Background(), nil, ChatOptions{}, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

func TestResolveModelCaches(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		n.Add(1)
		_ = json.NewEncoder(w).Encode(struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}{Data: []struct {
			ID string `json:"id"`
		}{{ID: "qwen38.nvfp4"}}})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	id, err := c.ResolveModel(context.Background())
	if err != nil || id != "qwen38.nvfp4" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	id2, err := c.ResolveModel(context.Background())
	if err != nil || id2 != id {
		t.Fatalf("second id=%q err=%v", id2, err)
	}
	if n.Load() != 1 {
		t.Fatalf("models requests %d want 1", n.Load())
	}
}
