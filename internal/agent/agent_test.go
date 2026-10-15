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
		Tools:     tools.NewRegistry(g, io.Discard, nil, nil, nil),
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

func setupCfg(t *testing.T, replies []llm.Message, confirm Confirmer, extra Config) (string, *replay, error) {
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
	extra.Client = client
	if extra.Tools == nil {
		extra.Tools = tools.NewRegistry(g, io.Discard, nil, nil, nil)
	}
	extra.Confirmer = confirm
	if extra.Out == nil {
		extra.Out = io.Discard
	}
	if extra.MaxSteps == 0 {
		extra.MaxSteps = 15
	}
	err = Run(context.Background(), extra, "sys", "do the task")
	return root, rep, err
}

func TestPlanGateBlocksMutating(t *testing.T) {
	root, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"x"}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, nil, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "plan not approved") {
		t.Fatalf("%q", got.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("mutating write should not have run")
	}
}

func TestPlanApprovalAllowsMutating(t *testing.T) {
	root, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"add a.txt","steps":["write a.txt"]}`)),
		assistant(call("2", "write_file", `{"path":"a.txt","content":"ok"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(b) != "ok" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestPlanRejectFeedsReason(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"x","steps":["y"]}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, stubConfirm{d: ui.Reject, reason: "too risky"}, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "too risky") {
		t.Fatalf("%q", got.Content)
	}
}

func TestContextElision(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(strings.Repeat("hello world\n", 80)), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	list := func(context.Context) ([]string, error) { return []string{"big.txt"}, nil }
	var replies []llm.Message
	for i := 0; i < 8; i++ {
		replies = append(replies, assistant(call(string(rune('a'+i)), "read_file", `{"path":"big.txt"}`)))
	}
	replies = append(replies, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	client, rep := newReplay(t, replies)
	err = Run(context.Background(), Config{
		Client:              client,
		Tools:               tools.NewRegistry(g, io.Discard, list, nil, nil),
		Confirmer:           stubConfirm{d: ui.Approve},
		Out:                 io.Discard,
		MaxSteps:            15,
		ContextBudgetTokens: 80,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, req := range rep.reqs {
		for _, m := range req {
			if m.Role == llm.RoleTool && m.Content == elidedOutput {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected an elided tool message")
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

type collectRec struct {
	mu    sync.Mutex
	msgs  []llm.Message
	acts  []ActionRecord
	files []tools.FileChange
}

func (c *collectRec) Message(_ context.Context, m llm.Message) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
	return nil
}
func (c *collectRec) Action(_ context.Context, a ActionRecord) error {
	c.mu.Lock()
	c.acts = append(c.acts, a)
	c.mu.Unlock()
	return nil
}
func (c *collectRec) FileChange(_ context.Context, f tools.FileChange) error {
	c.mu.Lock()
	c.files = append(c.files, f)
	c.mu.Unlock()
	return nil
}

func TestHistoryPrepended(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{{Role: llm.RoleAssistant, Content: "hi"}})
	err = Run(context.Background(), Config{
		Client:    client,
		Tools:     tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve},
		Out:       io.Discard,
		MaxSteps:  5,
		History:   []llm.Message{{Role: llm.RoleUser, Content: "earlier"}, {Role: llm.RoleAssistant, Content: "ok"}},
	}, "sys", "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.reqs) == 0 {
		t.Fatal("no requests")
	}
	roles := make([]llm.Role, len(rep.reqs[0]))
	for i, m := range rep.reqs[0] {
		roles[i] = m.Role
	}
	if len(roles) < 4 || roles[0] != llm.RoleSystem || roles[1] != llm.RoleUser || roles[2] != llm.RoleAssistant || roles[3] != llm.RoleUser {
		t.Fatalf("roles %v", roles)
	}
	if rep.reqs[0][1].Content != "earlier" || rep.reqs[0][3].Content != "now" {
		t.Fatalf("history %+v", rep.reqs[0])
	}
}

func TestRecorderGateAndFileChange(t *testing.T) {
	rec := &collectRec{}
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"x"}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, nil, Config{RequirePlan: true, Recorder: rec})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range rec.acts {
		if a.Decision == "gate_blocked" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want gate_blocked, acts=%+v", rec.acts)
	}

	rec2 := &collectRec{}
	_, _, err = setupCfg(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"a.txt","content":"hello"}`)),
		assistant(call("2", "finish", `{"summary":"done"}`)),
	}, nil, Config{Recorder: rec2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec2.files) != 1 || rec2.files[0].Path != "a.txt" || rec2.files[0].ExistedBefore {
		t.Fatalf("file changes %+v", rec2.files)
	}
	var sawUser, sawAsst, sawTool bool
	for _, m := range rec2.msgs {
		switch m.Role {
		case llm.RoleUser:
			sawUser = true
		case llm.RoleAssistant:
			sawAsst = true
		case llm.RoleTool:
			sawTool = true
		}
	}
	if !sawUser || !sawAsst || !sawTool {
		t.Fatalf("messages %+v", rec2.msgs)
	}
}
