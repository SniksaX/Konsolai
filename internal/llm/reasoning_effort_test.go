package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// effortServer rejects tools with any reasoning_effort except the named one,
// mimicking the reported OpenAI 400.
type effortServer struct {
	mu      sync.Mutex
	hits    int
	bodies  []map[string]any
	status  int
	allow   string
	message string
}

func newEffortServer(t *testing.T) (*Client, *effortServer) {
	t.Helper()
	es := &effortServer{allow: "none", message: "Function tools with reasoning_effort are not supported"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "gpt-6-luna", "max_model_len": 128000}}})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		es.mu.Lock()
		es.hits++
		es.bodies = append(es.bodies, body)
		status := es.status
		es.mu.Unlock()
		tools, hasTools := body["tools"].([]any)
		effort, hasEffort := body["reasoning_effort"].(string)
		if status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, es.message+" reasoning_effort 'none'")
			return
		}
		if len(tools) > 0 && (!hasEffort || effort != es.allow) || (hasTools && !hasEffort) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Function tools with reasoning_effort are not supported. To use function tools, set reasoning_effort to 'none'."}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "gpt-6-luna")
	c.PinContext(128000)
	c.SetRetry(0, 1)
	return c, es
}

func (es *effortServer) snapshot() (int, []map[string]any) {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.hits, append([]map[string]any(nil), es.bodies...)
}

func TestToolsReasoningEffortRetriesOnceWithTheNamedValue(t *testing.T) {
	resetReasoningFallback()
	t.Cleanup(resetReasoningFallback)
	c, es := newEffortServer(t)
	msgs := []Message{{Role: RoleUser, Content: "go"}}
	tools := toolSpecForTest("propose_plan")
	if _, _, err := c.Complete(context.Background(), msgs, tools, ChatOptions{}); err != nil {
		t.Fatalf("the tools+reasoning_effort 400 must be retried: %v", err)
	}
	hits, bodies := es.snapshot()
	if hits != 2 {
		t.Fatalf("want two attempts, got %d", hits)
	}
	if _, ok := bodies[0]["reasoning_effort"]; ok {
		t.Fatalf("first attempt must not send reasoning_effort: %v", bodies[0])
	}
	if bodies[1]["reasoning_effort"] != "none" {
		t.Fatalf("retry must send the value the message names: %v", bodies[1])
	}
	if c.LastReasoningFallback() != "none" {
		t.Fatalf("trace value %q", c.LastReasoningFallback())
	}
	// The fallback sticks for this model: the next tools request sends it at once.
	if _, _, err := c.Complete(context.Background(), msgs, tools, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	hits, bodies = es.snapshot()
	if hits != 3 || bodies[2]["reasoning_effort"] != "none" {
		t.Fatalf("fallback did not stick: hits %d body %v", hits, bodies[2])
	}
	// Without tools the model keeps its default reasoning (no field).
	if _, _, err := c.Complete(context.Background(), msgs, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	hits, bodies = es.snapshot()
	if hits != 4 {
		t.Fatalf("hits %d", hits)
	}
	if _, ok := bodies[3]["reasoning_effort"]; ok {
		t.Fatalf("a plain request must omit reasoning_effort: %v", bodies[3])
	}
}

func TestConfiguredReasoningEffortIsTopLevel(t *testing.T) {
	resetReasoningFallback()
	c, es := newEffortServer(t)
	c.SetReasoningEffort("high")
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	_, bodies := es.snapshot()
	if bodies[0]["reasoning_effort"] != "high" {
		t.Fatalf("configured effort not sent: %v", bodies[0])
	}
	c.SetReasoningEffort("default")
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "again"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	_, bodies = es.snapshot()
	if _, ok := bodies[1]["reasoning_effort"]; ok {
		t.Fatalf("default must omit the field: %v", bodies[1])
	}
}

func TestReasoningEffortNeverRetriesAuthOrRateLimits(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			resetReasoningFallback()
			c, es := newEffortServer(t)
			es.status = status
			_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, toolSpecForTest("propose_plan"), ChatOptions{})
			if err == nil {
				t.Fatal("want an error")
			}
			if hits, _ := es.snapshot(); hits != 1 {
				t.Fatalf("status %d must not be retried, hits %d", status, hits)
			}
		})
	}
}

func TestReasoningTruncationReadsClearly(t *testing.T) {
	resetReasoningFallback()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-6-luna","max_model_len":128000}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking hard\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":40}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "gpt-6-luna")
	c.PinContext(128000)
	c.SetReasoningEffort("high")
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{})
	if !errors.Is(err, ErrReasoningTruncated) {
		t.Fatalf("want ErrReasoningTruncated, got %v", err)
	}
}

func resetReasoningFallback() {
	reasoningFallback.Range(func(k, _ any) bool {
		reasoningFallback.Delete(k)
		return true
	})
}
