package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type toolChoiceServer struct {
	mu        sync.Mutex
	choices   []string
	thinking  []string
	rejects   int
	rejectsOn bool
}

func newToolChoiceServer(t *testing.T) (*Client, *toolChoiceServer) {
	t.Helper()
	ts := &toolChoiceServer{rejectsOn: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek", "max_model_len": 8192}}})
			return
		}
		var body struct {
			ToolChoice json.RawMessage `json:"tool_choice"`
			Thinking   json.RawMessage `json:"thinking"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		choice := strings.TrimSpace(string(body.ToolChoice))
		ts.mu.Lock()
		ts.choices = append(ts.choices, choice)
		ts.thinking = append(ts.thinking, strings.TrimSpace(string(body.Thinking)))
		reject := ts.rejectsOn && choice != "" && choice != `"auto"`
		if reject {
			ts.rejects++
		}
		ts.mu.Unlock()
		if reject {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Thinking mode does not support this tool_choice"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "deepseek")
	c.PinContext(8192)
	c.SetRetry(0, time.Millisecond)
	resetForcedToolChoice()
	t.Cleanup(resetForcedToolChoice)
	return c, ts
}

func (ts *toolChoiceServer) snapshot() ([]string, []string, int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]string(nil), ts.choices...), append([]string(nil), ts.thinking...), ts.rejects
}

func TestForcedToolChoice400RetriesOnceAndDowngrades(t *testing.T) {
	c, ts := newToolChoiceServer(t)
	opts := ChatOptions{ToolChoice: "propose_plan"}
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, toolSpecForTest("propose_plan"), opts); err != nil {
		t.Fatalf("the 400 must be retried: %v", err)
	}
	choices, _, rejects := ts.snapshot()
	if rejects != 1 {
		t.Fatalf("rejects %d choices %v", rejects, choices)
	}
	if len(choices) != 2 {
		t.Fatalf("want two attempts, got %v", choices)
	}
	if !strings.Contains(choices[0], "propose_plan") {
		t.Fatalf("first attempt did not name the tool: %v", choices)
	}
	if choices[1] != "" {
		t.Fatalf("the retry still sent tool_choice: %v", choices)
	}
	// The downgrade sticks for the rest of the process and the same model.
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "again"}}, toolSpecForTest("propose_plan"), opts); err != nil {
		t.Fatal(err)
	}
	choices, _, rejects = ts.snapshot()
	if rejects != 1 || choices[len(choices)-1] != "" {
		t.Fatalf("downgrade did not stick: rejects %d choices %v", rejects, choices)
	}
}

func TestAutoToolChoiceIsOmitted(t *testing.T) {
	c, ts := newToolChoiceServer(t)
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, toolSpecForTest("propose_plan"), ChatOptions{ToolChoice: "auto"}); err != nil {
		t.Fatal(err)
	}
	choices, _, rejects := ts.snapshot()
	if rejects != 0 || len(choices) != 1 || choices[0] != "" {
		t.Fatalf("auto mode sent a tool_choice: choices %v rejects %d", choices, rejects)
	}
}

func TestThinkingModeToggleInThePayload(t *testing.T) {
	c, ts := newToolChoiceServer(t)
	c.SetThinkingMode("disabled")
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	c.SetThinkingMode("enabled")
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	_, thinking, _ := ts.snapshot()
	if len(thinking) != 2 || thinking[0] != `{"type":"disabled"}` || thinking[1] != `{"type":"enabled"}` {
		t.Fatalf("thinking toggles %v", thinking)
	}
}

func resetForcedToolChoice() {
	forcedToolChoiceOff.Range(func(k, _ any) bool {
		forcedToolChoiceOff.Delete(k)
		return true
	})
}
