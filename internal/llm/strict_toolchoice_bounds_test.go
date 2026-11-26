package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// boundsServer is a strict thinking-mode provider for the P0-A retry path. It
// records every request, rejects a named tool_choice, and either answers the
// auto retry (ok) or stalls it until the caller cancels (stall).
type boundsServer struct {
	mu       sync.Mutex
	requests []string
	rejects  int
	stall    bool
}

func newBoundsServer(t *testing.T, stall bool) (*Client, *boundsServer) {
	t.Helper()
	bs := &boundsServer{stall: stall}
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
		bs.mu.Lock()
		bs.requests = append(bs.requests, choice)
		reject := choice != "" && choice != `"auto"`
		if reject {
			bs.rejects++
		}
		stall := bs.stall
		bs.mu.Unlock()
		if reject {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Thinking mode does not support this tool_choice"}}`)
			return
		}
		if stall {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "deepseek-flash")
	c.PinContext(8192)
	c.SetRetry(0, time.Millisecond)
	resetForcedToolChoice()
	t.Cleanup(resetForcedToolChoice)
	return c, bs
}

func (bs *boundsServer) snapshot() ([]string, int) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return append([]string(nil), bs.requests...), bs.rejects
}

// TestStrictToolChoiceAutoRetryAnswers proves the allowed side: the named
// attempt is rejected once, the retry carries no tool_choice, and Complete
// returns the provider's answer.
func TestStrictToolChoiceAutoRetryAnswers(t *testing.T) {
	c, bs := newBoundsServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, _, err := c.Complete(ctx, []Message{{Role: RoleUser, Content: "go"}}, toolSpecForTest("propose_plan"), ChatOptions{ToolChoice: "propose_plan"})
	if err != nil {
		t.Fatalf("the auto retry must answer: %v", err)
	}
	if msg.Content != "ok" {
		t.Fatalf("content %q", msg.Content)
	}
	requests, rejects := bs.snapshot()
	if rejects != 1 || len(requests) != 2 {
		t.Fatalf("requests %v rejects %d", requests, rejects)
	}
	if !strings.Contains(requests[0], "propose_plan") || requests[1] != "" {
		t.Fatalf("the retry did not drop tool_choice: %v", requests)
	}
}

// TestStrictToolChoiceStalledRetryHonorsContext explains the P0-A duration: if
// the provider accepts the auto retry and never responds, Complete must return
// when the caller's context ends, not wait for the streaming idle timeout.
func TestStrictToolChoiceStalledRetryHonorsContext(t *testing.T) {
	c, bs := newBoundsServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := c.Complete(ctx, []Message{{Role: RoleUser, Content: "go"}}, toolSpecForTest("propose_plan"), ChatOptions{ToolChoice: "propose_plan"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a stalled retry must return an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the context deadline, got %v", err)
	}
	// The default stream idle timeout is 120s; a canceled call must not wait
	// for it. The 3s bound is a generous ceiling, not a sleep.
	if elapsed > 3*time.Second {
		t.Fatalf("the stalled retry waited %s", elapsed)
	}
	requests, rejects := bs.snapshot()
	if rejects != 1 || len(requests) != 2 {
		t.Fatalf("requests %v rejects %d", requests, rejects)
	}
}

// TestPersistentStrictRejectionStops proves the refused side: a provider that
// rejects the auto retry too returns an error after exactly one downgrade, with
// no retry loop.
func TestPersistentStrictRejectionStops(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek-flash", "max_model_len": 8192}}})
			return
		}
		mu.Lock()
		attempts++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Thinking mode does not support this tool_choice"}}`)
	}))
	t.Cleanup(counting.Close)
	c := New(counting.URL+"/v1", "deepseek-flash")
	c.PinContext(8192)
	c.SetRetry(0, time.Millisecond)
	resetForcedToolChoice()
	t.Cleanup(resetForcedToolChoice)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := c.Complete(ctx, []Message{{Role: RoleUser, Content: "go"}}, toolSpecForTest("propose_plan"), ChatOptions{ToolChoice: "propose_plan"}); err == nil {
		t.Fatal("a persistent rejection must return an error")
	}
	mu.Lock()
	got := attempts
	mu.Unlock()
	if got != 2 {
		t.Fatalf("want the named attempt plus one auto retry, got %d", got)
	}
}
