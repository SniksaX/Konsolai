package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageReasoningTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"completion_tokens_details":{"reasoning_tokens":7100}}}`,
		)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "main-model")
	_, u, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if u.ReasoningTokens != 7100 || u.CompletionTokens != 20 {
		t.Fatalf("%+v", u)
	}
}

func TestUsageReasoningFromStream(t *testing.T) {
	reason := strings.Repeat("a", 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"choices":[{"delta":{"reasoning":"`+reason+`"}}]}`,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2}}`,
		)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "main-model")
	want := c.EstimateReasoningTokens(len(reason))
	_, u, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if u.ReasoningTokens != want || want != 3 {
		t.Fatalf("got %d want %d", u.ReasoningTokens, want)
	}
}

func TestThinkingBudgetSent(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		writeSSE(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL+"/v1", "main-model")
	c.SetThinkKwargs(true)
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{
		Thinking: true, ThinkingBudget: 4096, MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	var kw struct {
		EnableThinking bool `json:"enable_thinking"`
		ThinkingBudget int  `json:"thinking_budget"`
	}
	if err := json.Unmarshal(raw["chat_template_kwargs"], &kw); err != nil {
		t.Fatal(err)
	}
	if !kw.EnableThinking || kw.ThinkingBudget != 4096 {
		t.Fatalf("kwargs %+v", kw)
	}
	var max int
	if err := json.Unmarshal(raw["max_tokens"], &max); err != nil || max != 100 {
		t.Fatalf("max_tokens %s", raw["max_tokens"])
	}

	plain := New(srv.URL+"/v1", "main-model")
	_, _, err = plain.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{
		Thinking: true, ThinkingBudget: 4096, MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw = map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw["max_tokens"], &max); err != nil || max != 4196 {
		t.Fatalf("raised max_tokens %s", raw["max_tokens"])
	}
	if _, ok := raw["chat_template_kwargs"]; ok {
		t.Fatalf("kwargs sent without support: %s", raw["chat_template_kwargs"])
	}
}

func TestThinkingTruncationContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"choices":[{"delta":{"content":"partial plan"},"finish_reason":"length"}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "main-model")
	msg, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{Thinking: true})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "partial plan" {
		t.Fatalf("%q", msg.Content)
	}
	_, _, err = c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("non-thinking %v", err)
	}
}
