package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

type stubConfirm struct {
	d      ui.Decision
	reason string
}

func (s stubConfirm) Confirm(string, string) (ui.Decision, string, error) {
	return s.d, s.reason, nil
}

type replay struct {
	t       *testing.T
	mu      sync.Mutex
	replies []llm.Message
	reqs    [][]llm.Message
}

func newReplay(t *testing.T, replies []llm.Message) (*llm.Client, *replay) {
	t.Helper()
	r := &replay{t: t, replies: replies}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", r.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return llm.New(srv.URL+"/v1", "test"), r
}

func (r *replay) serve(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Messages []llm.Message `json:"messages"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, body.Messages)
	i := len(r.reqs) - 1
	r.mu.Unlock()
	if i >= len(r.replies) {
		http.Error(w, "no more replies", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Choices []struct {
			Message llm.Message `json:"message"`
		} `json:"choices"`
	}{Choices: []struct {
		Message llm.Message `json:"message"`
	}{{Message: r.replies[i]}}})
}

func (r *replay) lastTool() llm.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) < 2 {
		r.t.Fatalf("want >=2 requests, got %d", len(r.reqs))
	}
	msgs := r.reqs[len(r.reqs)-1]
	return msgs[len(msgs)-1]
}

func call(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}
}

func assistant(calls ...llm.ToolCall) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}
}

func setup(t *testing.T, replies []llm.Message, confirm Confirmer, maxSteps int) (string, *replay, error) {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, replies)
	if confirm == nil {
		confirm = stubConfirm{d: ui.Approve}
	}
	if maxSteps == 0 {
		maxSteps = 15
	}
	err = Run(context.Background(), Config{
		Client:    client,
		Tools:     tools.NewRegistry(g, io.Discard, nil),
		Confirmer: confirm,
		Out:       io.Discard,
		MaxSteps:  maxSteps,
	}, "sys", "do the task")
	return root, rep, err
}

func TestRunWriteFileThenFinish(t *testing.T) {
	root, _, err := setup(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"hello"}`)),
		assistant(call("2", "finish", `{"summary":"wrote a.txt"}`)),
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
}

func TestRunRejectFeedsReason(t *testing.T) {
	_, rep, err := setup(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"x"}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, stubConfirm{d: ui.Reject, reason: "not that file"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if got.Role != llm.RoleTool || !strings.Contains(got.Content, "not that file") {
		t.Fatalf("last tool %+v", got)
	}
}

func TestRunQuitAborted(t *testing.T) {
	_, _, err := setup(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"x"}`)),
	}, stubConfirm{d: ui.Quit}, 0)
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("got %v", err)
	}
}

func TestRunUnknownToolContinues(t *testing.T) {
	_, rep, err := setup(t, []llm.Message{
		assistant(call("1", "not_a_tool", `{}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if got.Role != llm.RoleTool || !strings.HasPrefix(got.Content, "error:") {
		t.Fatalf("last tool %+v", got)
	}
}

func TestRunMaxSteps(t *testing.T) {
	_, _, err := setup(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
	}, nil, 2)
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("got %v", err)
	}
}

func TestRunRepeatedFailureNote(t *testing.T) {
	fail := `{"cmd":"false"}`
	_, rep, err := setup(t, []llm.Message{
		assistant(call("1", "run_shell", fail)),
		assistant(call("2", "run_shell", fail)),
		{Role: llm.RoleAssistant, Content: "stopped"},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "already failed") {
		t.Fatalf("want already-failed note, got %q", got.Content)
	}
}

func TestRunCDPrepareError(t *testing.T) {
	_, rep, err := setup(t, []llm.Message{
		assistant(call("1", "run_shell", `{"cmd":"cd src"}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if got.Role != llm.RoleTool || !strings.Contains(got.Content, "do not use cd") {
		t.Fatalf("last tool %+v", got)
	}
}
