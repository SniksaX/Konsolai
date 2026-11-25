package agent

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

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// strictToolChoiceServer returns the DeepSeek 400 whenever tool_choice names a
// function or asks for a required tool, and records every request's
// tool_choice field. A non-empty auto field is also rejected.
type strictToolChoiceServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	replies  []llm.Message
	choices  []string
	reqs     [][]llm.Message
	hits     int
	accepted int
	rejects  int
}

func newStrictToolChoiceServer(t *testing.T, replies ...llm.Message) (*llm.Client, *strictToolChoiceServer) {
	t.Helper()
	ss := &strictToolChoiceServer{replies: replies}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "deepseek", "max_model_len": 8192}}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ToolChoice json.RawMessage `json:"tool_choice"`
			Messages   []llm.Message   `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ss.mu.Lock()
		n := ss.accepted
		ss.hits++
		choice := strings.TrimSpace(string(body.ToolChoice))
		ss.choices = append(ss.choices, choice)
		ss.reqs = append(ss.reqs, body.Messages)
		if choice != "" && choice != `"auto"` {
			ss.rejects++
			ss.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Thinking mode does not support this tool_choice"}}`)
			return
		}
		ss.accepted++
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
	})
	ss.srv = httptest.NewServer(mux)
	t.Cleanup(ss.srv.Close)
	c := llm.New(ss.srv.URL+"/v1", "deepseek")
	c.PinContext(8192)
	return c, ss
}

func (ss *strictToolChoiceServer) snapshot() ([]string, [][]llm.Message, int) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	reqs := make([][]llm.Message, len(ss.reqs))
	copy(reqs, ss.reqs)
	return append([]string(nil), ss.choices...), reqs, ss.rejects
}

func strictRun(t *testing.T, client *llm.Client, mode string) error {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard,
		RequirePlan: true, ThinkingPolicy: ThinkPlanStep,
		MaxSteps: 8, Intent: "build", ToolChoiceMode: mode,
	}, "sys", "change a file")
}

// planReplies drives a gate reject (write_file before the plan), then one
// forced-plan reply, then finish.
func planReplies() []llm.Message {
	return []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"a"}`)),
		assistant(call("2", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"],"commands":[]}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}
}

func TestForcedPlanWorksAgainstTheStrictDeepSeekServer(t *testing.T) {
	client, ss := newStrictToolChoiceServer(t, planReplies()...)
	if err := strictRun(t, client, "auto"); err != nil {
		t.Fatalf("auto run: %v", err)
	}
	choices, _, rejects := ss.snapshot()
	if rejects != 0 {
		t.Fatalf("the strict server rejected %d requests: %v", rejects, choices)
	}
	for _, c := range choices {
		if c != "" && c != `"auto"` {
			t.Fatalf("auto mode sent tool_choice %s", c)
		}
	}
}

// TestNamedModeRetriesOnToolChoice400: a named mode against a provider that
// rejects tool_choice retries once with auto, then finishes the turn.
func TestNamedModeRetriesOnToolChoice400(t *testing.T) {
	client, ss := newStrictToolChoiceServer(t, planReplies()...)
	if err := strictRun(t, client, ""); err != nil {
		t.Fatalf("named mode must retry on the tool_choice 400: %v", err)
	}
	choices, _, rejects := ss.snapshot()
	if rejects != 1 {
		t.Fatalf("want exactly one rejection, got %d: %v", rejects, choices)
	}
	named, downgraded := 0, 0
	for _, c := range choices {
		switch {
		case c == "":
			downgraded++
		case c != `"auto"`:
			named++
		}
	}
	if named != 1 {
		t.Fatalf("want one named attempt before the downgrade: %v", choices)
	}
	if downgraded == 0 {
		t.Fatalf("the retry never omitted tool_choice: %v", choices)
	}
}

func TestMissingForcedPlanIsNudgedOnceThenPasses(t *testing.T) {
	replies := []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"a"}`)),
		{Role: llm.RoleAssistant, Content: "I cannot plan."},
		assistant(call("2", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"],"commands":[]}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}
	client, ss := newStrictToolChoiceServer(t, replies...)
	if err := strictRun(t, client, "auto"); err != nil {
		t.Fatalf("nudged run: %v", err)
	}
	_, reqs, rejects := ss.snapshot()
	if rejects != 0 {
		t.Fatalf("strict rejects %d", rejects)
	}
	nudges := 0
	if len(reqs) > 0 {
		for _, m := range reqs[len(reqs)-1] {
			if m.Role == llm.RoleUser && m.Content == ForcedPlanNote {
				nudges++
			}
		}
	}
	if nudges != 1 {
		t.Fatalf("want exactly one nudge, saw %d", nudges)
	}
}

func TestMissingForcedPlanFailsAfterTheNudge(t *testing.T) {
	replies := []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"a"}`)),
		{Role: llm.RoleAssistant, Content: "I cannot plan."},
		{Role: llm.RoleAssistant, Content: "Still no plan."},
	}
	client, ss := newStrictToolChoiceServer(t, replies...)
	err := strictRun(t, client, "auto")
	if !errors.Is(err, ErrNoPlan) {
		t.Fatalf("want ErrNoPlan, got %v", err)
	}
	choices, _, rejects := ss.snapshot()
	if rejects != 0 || len(choices) < 3 {
		t.Fatalf("choices %v rejects %d", choices, rejects)
	}
}
