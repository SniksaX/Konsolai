package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

func (s stubConfirm) ConfirmForced(summary, preview string) (ui.Decision, string, error) {
	return s.Confirm(summary, preview)
}

type replay struct {
	t            *testing.T
	mu           sync.Mutex
	replies      []llm.Message
	reqs         [][]llm.Message
	tools        [][]llm.ToolSpec
	think        []bool
	toolChoices  []json.RawMessage
	maxTokens    []int
	promptTokens []int
	truncates    []bool
	modelLen     int
}

func newReplay(t *testing.T, replies []llm.Message) (*llm.Client, *replay) {
	t.Helper()
	r := &replay{t: t, replies: replies}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, req *http.Request) {
		win := r.modelLen
		if win <= 0 {
			win = 94208
		}
		_ = json.NewEncoder(w).Encode(struct {
			Data []struct {
				ID          string `json:"id"`
				MaxModelLen int    `json:"max_model_len"`
			} `json:"data"`
		}{Data: []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		}{{ID: "test", MaxModelLen: win}}})
	})
	mux.HandleFunc("/v1/chat/completions", r.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "test")
	c.SetThinkKwargs(true)
	return c, r
}

func (r *replay) serve(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Messages           []llm.Message   `json:"messages"`
		Tools              []llm.ToolSpec  `json:"tools"`
		ToolChoice         json.RawMessage `json:"tool_choice"`
		MaxTokens          int             `json:"max_tokens"`
		ChatTemplateKwargs struct {
			EnableThinking bool `json:"enable_thinking"`
		} `json:"chat_template_kwargs"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, body.Messages)
	r.tools = append(r.tools, body.Tools)
	r.toolChoices = append(r.toolChoices, body.ToolChoice)
	r.think = append(r.think, body.ChatTemplateKwargs.EnableThinking)
	r.maxTokens = append(r.maxTokens, body.MaxTokens)
	i := len(r.reqs) - 1
	pt := 1000
	if r.promptTokens != nil {
		pt = 0
		if i < len(r.promptTokens) {
			pt = r.promptTokens[i]
		}
	}
	r.mu.Unlock()
	if i >= len(r.replies) {
		http.Error(w, "no more replies", http.StatusInternalServerError)
		return
	}
	if i < len(r.truncates) && r.truncates[i] {
		payload, err := json.Marshal(map[string]any{
			"choices": []map[string]any{{
				"delta":         map[string]any{"content": r.replies[i].Content},
				"finish_reason": "length",
			}},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Choices []struct {
			Message llm.Message `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}{Choices: []struct {
		Message llm.Message `json:"message"`
	}{{Message: r.replies[i]}}, Usage: struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	}{PromptTokens: pt, CompletionTokens: 200}})
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
	fail := `{"cmd":"cp /no-such-src /no-such-dst"}`
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
	if extra.Guard == nil {
		extra.Guard = g
	}
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
	if !strings.Contains(got.Content, "call propose_plan first") {
		t.Fatalf("%q", got.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("mutating write should not have run")
	}
}

func TestPlanApprovalAllowsMutating(t *testing.T) {
	root, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"add a.txt","steps":["write a.txt"],"files":["a.txt"]}`)),
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
		assistant(call("1", "propose_plan", `{"summary":"x","steps":["y"],"files":["a.txt"]}`)),
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
	rep.promptTokens = []int{32000, 32000, 32000, 32000, 32000, 32000, 32000, 32000, 32000}
	err = Run(context.Background(), Config{
		Client:    client,
		Tools:     tools.NewRegistry(g, io.Discard, list, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve},
		Out:       io.Discard,
		MaxSteps:  15,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, req := range rep.reqs {
		for _, m := range req {
			if m.Role == llm.RoleTool && isElided(m.Content) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected an elided tool message")
	}
}

func samePrefix(prev, next []llm.Message) bool {
	if len(next) < len(prev) {
		return false
	}
	for i := range prev {
		if prev[i].Role != next[i].Role || prev[i].Content != next[i].Content || prev[i].ToolCallID != next[i].ToolCallID {
			return false
		}
		if len(prev[i].ToolCalls) != len(next[i].ToolCalls) {
			return false
		}
		for j := range prev[i].ToolCalls {
			if prev[i].ToolCalls[j].ID != next[i].ToolCalls[j].ID ||
				prev[i].ToolCalls[j].Function.Name != next[i].ToolCalls[j].Function.Name ||
				prev[i].ToolCalls[j].Function.Arguments != next[i].ToolCalls[j].Function.Arguments {
				return false
			}
		}
	}
	return true
}

func TestHysteresisElisionOnePass(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("hello world\n", 400)
	const nfiles = 14
	for i := 0; i < nfiles; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(big), 0644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var replies []llm.Message
	for i := 0; i < nfiles; i++ {
		replies = append(replies, assistant(call(fmt.Sprintf("c%d", i), "read_file", fmt.Sprintf(`{"path":"f%d.txt"}`, i))))
	}
	replies = append(replies, llm.Message{Role: llm.RoleAssistant, Content: "done"})
	client, rep := newReplay(t, replies)
	rep.modelLen = 32768
	rep.promptTokens = make([]int, len(replies))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	reg := tools.NewRegistry(g, io.Discard, nil, nil, nil).Named("read_file")
	var errW strings.Builder
	err = Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, MaxSteps: 15, Trace: true, Err: &errW, Intent: "act",
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	window := client.ContextWindow()
	if window != 32768 {
		t.Fatalf("window %d", window)
	}
	passes := 0
	for i := 1; i < len(rep.reqs); i++ {
		if samePrefix(rep.reqs[i-1], rep.reqs[i]) {
			continue
		}
		nElided := 0
		for _, m := range rep.reqs[i] {
			if m.Role == llm.RoleTool && isElided(m.Content) {
				nElided++
			}
		}
		if nElided < 2 {
			t.Fatalf("prefix changed at %d without a batch elide (%d)", i, nElided)
		}
		est := client.EstimatePromptTokens(rep.reqs[i], rep.tools[i])
		if float64(est) > contextTarget*float64(window)+1 {
			t.Fatalf("one pass should reach <=40%%, est=%d window=%d", est, window)
		}
		passes++
	}
	if passes < 1 {
		t.Fatal("expected a hysteresis pass")
	}
	if !strings.Contains(errW.String(), "elided") || !strings.Contains(errW.String(), "tool outputs") {
		t.Fatalf("trace %q", errW.String())
	}
}

func TestElidedMarkerDeterministic(t *testing.T) {
	a := elidedMarker("read_file", elideSummary("hello world\nmore"))
	b := elidedMarker("read_file", elideSummary("hello world\nmore"))
	if a != b || a != "[tool output elided: read_file hello world]" {
		t.Fatalf("%q %q", a, b)
	}
	if FormatElide(ElideStats{Count: 9, Before: 57467, After: 35799}, 94208) != "  elided 9 tool outputs (ctx 61% -> 38%)" {
		t.Fatalf("%q", FormatElide(ElideStats{Count: 9, Before: 57467, After: 35799}, 94208))
	}
}

func TestMaxTokensComputation(t *testing.T) {
	if ProfileCap("answer") != 4096 || ProfileCap("lookup") != 4096 || ProfileCap("act") != 4096 {
		t.Fatal("small caps")
	}
	if ProfileCap("build") != 12288 {
		t.Fatal("build cap")
	}
	if MaxTokensFor(94208, 50000, 4096) != 4096 {
		t.Fatalf("capped %d", MaxTokensFor(94208, 50000, 4096))
	}
	if MaxTokensFor(32768, 30000, 4096) != 1744 {
		t.Fatalf("room %d", MaxTokensFor(32768, 30000, 4096))
	}
	_, rep, err := setupCfg(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: "ok"},
	}, nil, Config{Intent: "act"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.maxTokens) == 0 || rep.maxTokens[0] <= 0 || rep.maxTokens[0] > 4096 {
		t.Fatalf("max_tokens %v", rep.maxTokens)
	}
}

func TestTruncationRecovery(t *testing.T) {
	var out bytes.Buffer
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: "cut off"},
		{Role: llm.RoleAssistant, Content: "full answer after retry"},
	})
	rep.truncates = []bool{true}
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: &out, MaxSteps: 5, Intent: "act",
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "full answer after retry") {
		t.Fatalf("%q", out.String())
	}
	if len(rep.reqs) != 2 {
		t.Fatalf("reqs %d", len(rep.reqs))
	}
	if len(rep.maxTokens) < 2 || rep.maxTokens[1] <= rep.maxTokens[0] {
		t.Fatalf("max_tokens not raised %v", rep.maxTokens)
	}
}

func TestTruncationTwiceOverflows(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: "cut"},
		{Role: llm.RoleAssistant, Content: "cut again"},
	})
	rep.truncates = []bool{true, true}
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, MaxSteps: 5, Intent: "act",
	}, "sys", "task")
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("got %v", err)
	}
}

func TestContextOverflowError(t *testing.T) {
	huge := strings.Repeat("word ", 80000)
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := newReplay(t, []llm.Message{{Role: llm.RoleAssistant, Content: "ok"}})
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, MaxSteps: 3, Intent: "act",
	}, huge, huge)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("got %v", err)
	}
	if err.Error() != "konsolai: the conversation no longer fits the model's context; start a new session with `new`." {
		t.Fatalf("msg %q", err.Error())
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

func TestToolLayoutIndents(t *testing.T) {
	var out bytes.Buffer
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, nil, Config{Out: &out, MaxSteps: 4, Intent: "act"})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "  ▸") {
		t.Fatalf("tool call indent: %q", s)
	}
	if !strings.Contains(s, "    ✓") && !strings.Contains(s, "    ✗") {
		t.Fatalf("meta indent: %q", s)
	}
}

func TestPrepareRejectPrintsBothLines(t *testing.T) {
	var out bytes.Buffer
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "run_shell", `{"cmd":"cd src"}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, nil, Config{Out: &out, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "▸") || !strings.Contains(s, "run_shell") || !strings.Contains(s, "cd src") {
		t.Fatalf("missing call line: %q", s)
	}
	if !strings.Contains(s, "✗ rejected:") || !strings.Contains(s, "do not use cd") {
		t.Fatalf("missing reject line: %q", s)
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

func TestDisplayMetaFallback(t *testing.T) {
	got := displayMeta(tools.Result{Output: "error: is a directory", IsError: true})
	if got != "is a directory" {
		t.Fatalf("%q", got)
	}
	if displayMeta(tools.Result{Meta: "74 B"}) != "74 B" {
		t.Fatal("keep meta")
	}
}

func TestTrimLeadingBlanks(t *testing.T) {
	if trimLeadingBlanks("\n\nhello\n") != "hello\n" {
		t.Fatalf("%q", trimLeadingBlanks("\n\nhello\n"))
	}
}

func TestEmptyAnswer(t *testing.T) {
	_, _, err := setup(t, []llm.Message{{Role: llm.RoleAssistant, Content: "\n\n"}}, nil, 0)
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("%v", err)
	}
}

func TestDuplicateReadonlySkipped(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	var buf strings.Builder
	err = Run(context.Background(), Config{
		Client:    client,
		Tools:     tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve},
		Out:       &buf,
		MaxSteps:  5,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "duplicate, skipped") {
		t.Fatalf("%q", buf.String())
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "already made this exact call") {
		t.Fatalf("%q", got.Content)
	}
}

type recConfirm struct {
	forced int
	d      ui.Decision
}

func (r *recConfirm) Confirm(string, string) (ui.Decision, string, error) {
	return r.d, "", nil
}
func (r *recConfirm) ConfirmForced(string, string) (ui.Decision, string, error) {
	r.forced++
	return r.d, "", nil
}

func TestOutsidePlanForcedConfirm(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	rc := &recConfirm{d: ui.Approve}
	client, _ := newReplay(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"]}`)),
		assistant(call("2", "write_file", `{"path":"b.txt","content":"x"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	})
	err = Run(context.Background(), Config{
		Client:      client,
		Tools:       tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer:   rc,
		Out:         io.Discard,
		MaxSteps:    8,
		RequirePlan: true,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	if rc.forced != 1 {
		t.Fatalf("forced %d", rc.forced)
	}
}

func TestOutsidePlanYesPrintsWarning(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := newReplay(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"]}`)),
		assistant(call("2", "write_file", `{"path":"b.txt","content":"x"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	})
	var buf strings.Builder
	err = Run(context.Background(), Config{
		Client:      client,
		Tools:       tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer:   stubConfirm{d: ui.Approve},
		Out:         &buf,
		MaxSteps:    8,
		RequirePlan: true,
		AutoApprove: true,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "⚠ outside plan:") {
		t.Fatalf("%q", buf.String())
	}
}

func TestFailedReadDirShowsMeta(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "ok"},
	})
	var buf strings.Builder
	err = Run(context.Background(), Config{
		Client:    client,
		Tools:     tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve},
		Out:       &buf,
		MaxSteps:  5,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "is a directory") {
		t.Fatalf("want meta, got %q", buf.String())
	}
}

func TestUntilPlanThinkingSchedule(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "propose_plan", `{"summary":"add a.txt","steps":["write","test"],"files":["a.txt"]}`)),
		assistant(call("3", "write_file", `{"path":"a.txt","content":"x"}`)),
		assistant(call("4", "run_shell", `{"cmd":"false"}`)),
		assistant(call("5", "write_file", `{"path":"a.txt","content":"y"}`)),
		assistant(call("6", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan:    true,
		AutoApprove:    true,
		ThinkingPolicy: ThinkUntilPlan,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, false, false, true, false}
	if len(rep.think) != len(want) {
		t.Fatalf("think calls %v want %v", rep.think, want)
	}
	for i, g := range rep.think {
		if g != want[i] {
			t.Fatalf("think[%d]=%v want %v (%v)", i, g, want[i], rep.think)
		}
	}
}

func TestPlanStepThinkingSchedule(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "propose_plan", `{"summary":"add a.txt","steps":["write"],"files":["a.txt"]}`)),
		assistant(call("3", "propose_plan", `{"summary":"add a.txt","steps":["write"],"files":["a.txt"]}`)),
		assistant(call("4", "write_file", `{"path":"a.txt","content":"x"}`)),
		assistant(call("5", "run_shell", `{"cmd":"false"}`)),
		assistant(call("6", "write_file", `{"path":"a.txt","content":"y"}`)),
		assistant(call("7", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan:    true,
		AutoApprove:    true,
		ThinkingPolicy: ThinkPlanStep,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, false, true, false, false, true, false}
	if len(rep.think) != len(want) {
		t.Fatalf("think calls %v want %v", rep.think, want)
	}
	for i, g := range rep.think {
		if g != want[i] {
			t.Fatalf("think[%d]=%v want %v (%v)", i, g, want[i], rep.think)
		}
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(rep.toolChoices[2], &choice); err != nil || choice.Function.Name != "propose_plan" {
		t.Fatalf("retry tool_choice %s", rep.toolChoices[2])
	}
	if !samePrefix(rep.reqs[1], rep.reqs[2]) {
		t.Fatal("plan retry must re-issue the same messages")
	}
}

func TestPlanStepForcedPlanThinks(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		assistant(call("4", "propose_plan", `{"summary":"x","steps":["y"],"files":["a.txt"]}`)),
		assistant(call("5", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, ExploreBudget: 1, ExploreNote: ExploreNoteBuild, MaxSteps: 8,
		ThinkingPolicy: ThinkPlanStep,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.think) < 4 {
		t.Fatalf("think %v", rep.think)
	}
	if !rep.think[3] {
		t.Fatalf("forced plan should think: %v", rep.think)
	}
}

func TestFormatSummaryAndTrace(t *testing.T) {
	s := Stats{
		Steps:            12,
		ModelTime:        41200 * time.Millisecond,
		ToolTime:         3100 * time.Millisecond,
		PromptTokens:     23400,
		CompletionTokens: 2100,
	}
	got := FormatSummary("build", s)
	want := "12 steps · 44.3s (model 41.2s, tools 3.1s) · 23.4k → 2.1k tok"
	if got != want {
		t.Fatalf("summary %q want %q", got, want)
	}
	tr := FormatTrace(2, true, 12300*time.Millisecond, 58400, 300, 94200, []string{"tool_a", "tool_b"})
	want = "  [step 2] think=on  12.3s  in=58.4k out=0.3k  ctx 62% (58.4k/94.2k)  -> tool_a, tool_b"
	if tr != want {
		t.Fatalf("trace %q want %q", tr, want)
	}
	tr = FormatTrace(1, false, time.Second, 0, 0, 32768, nil)
	if !strings.Contains(tr, "think=off") || !strings.Contains(tr, "ctx 0%") || !strings.Contains(tr, "->") {
		t.Fatalf("off %q", tr)
	}
	labels := FormatTraceCalls([]llm.ToolCall{call("1", "export_doc", strings.Repeat("a", 4100))})
	if len(labels) != 1 || labels[0] != "export_doc(args 4.1KB)" {
		t.Fatalf("labels %v", labels)
	}
	small := FormatTraceCalls([]llm.ToolCall{call("1", "export_doc", `{"path":"docs/OVERVIEW.pdf","format":"pdf"}`)})
	if len(small) != 1 || !strings.HasPrefix(small[0], "export_doc(args ") || !strings.HasSuffix(small[0], "B)") {
		t.Fatalf("small %v", small)
	}
}

func TestRunAccumulatesStats(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var st Stats
	var errW strings.Builder
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "finish", `{"summary":"ok"}`)),
	}, nil, Config{
		Stats:      &st,
		Err:        &errW,
		Intent:     "lookup",
		Trace:      true,
		RouterTime: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.Steps != 2 {
		t.Fatalf("steps %d", st.Steps)
	}
	if st.PromptTokens != 2000 || st.CompletionTokens != 400 {
		t.Fatalf("tokens %+v", st)
	}
	if st.LastPromptTokens != 1000 {
		t.Fatalf("last prompt %d", st.LastPromptTokens)
	}
	if st.RouterTime != time.Second {
		t.Fatalf("router %s", st.RouterTime)
	}
	if st.ModelTime <= 0 || st.ToolTime <= 0 {
		t.Fatalf("times %+v", st)
	}
	if !strings.Contains(errW.String(), "2.0k → 400 tok") {
		t.Fatalf("summary %q", errW.String())
	}
	if !strings.Contains(errW.String(), "[step 1]") || !strings.Contains(errW.String(), "list_dir") {
		t.Fatalf("trace %q", errW.String())
	}
	if !strings.Contains(errW.String(), "in=1.0k out=0.2k") {
		t.Fatalf("per-call tokens %q", errW.String())
	}
	if !strings.Contains(errW.String(), "2.0k → 400 tok") {
		t.Fatalf("totals %q", errW.String())
	}
}

type countConfirm struct {
	n, forced int
	d         ui.Decision
}

func (c *countConfirm) Confirm(string, string) (ui.Decision, string, error) {
	c.n++
	return c.d, "", nil
}
func (c *countConfirm) ConfirmForced(string, string) (ui.Decision, string, error) {
	c.forced++
	return c.d, "", nil
}

func TestInPlanAutoApprove(t *testing.T) {
	cc := &countConfirm{d: ui.Approve}
	var buf strings.Builder
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["write","touch"],"files":["a.txt"],"commands":["touch  x"]}`)),
		assistant(call("2", "write_file", `{"path":"a.txt","content":"hi"}`)),
		assistant(call("3", "run_shell", `{"cmd":"touch x"}`)),
		assistant(call("4", "finish", `{"summary":"done"}`)),
	}, cc, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if cc.n != 1 {
		t.Fatalf("confirm %d want 1 (plan only)", cc.n)
	}
	if cc.forced != 0 {
		t.Fatalf("forced %d", cc.forced)
	}
	_ = buf
}

func TestInPlanMarkerAndWhitespace(t *testing.T) {
	var buf strings.Builder
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["t"],"files":["a.txt"],"commands":["touch   x"]}`)),
		assistant(call("2", "run_shell", `{"cmd":"touch x"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(in plan)") {
		t.Fatalf("%q", buf.String())
	}
}

func TestInPlanCmdSegments(t *testing.T) {
	cmds := []string{"npm init -y", "npm install express"}
	if !inPlanCmd(cmds, "npm init -y && npm install express") {
		t.Fatal("joined && should be in plan")
	}
	if inPlanCmd(cmds, "npm init -y && npm install lodash") {
		t.Fatal("undeclared segment should be outside")
	}
	if !inPlanCmd([]string{`echo "a && b"`}, `echo "a && b"`) {
		t.Fatal("quoted && must stay one command")
	}
	if inPlanCmd(cmds, `echo "npm init -y && npm install express"`) {
		t.Fatal("quoted && must not split into declared segments")
	}
	if !inPlanCmd([]string{"ss -ltnp", "grep 80"}, "ss -ltnp | grep 80") {
		t.Fatal("read-only pipeline is in plan (R1)")
	}
	if !inPlanCmd([]string{"ss -ltnp | grep 80"}, "ss -ltnp | grep 80") {
		t.Fatal("declared pipeline should match")
	}
	if !inPlanCmd([]string{"tsc --init"}, "npx tsc --init") {
		t.Fatal("npx tsc --init should match tsc --init")
	}
	if !inPlanCmd([]string{"npx tsc --init"}, "tsc --init") {
		t.Fatal("tsc --init should match npx tsc --init")
	}
	if !inPlanCmd([]string{"tsc --init"}, "npx -y tsc --init") {
		t.Fatal("npx -y should match")
	}
	if !inPlanCmd([]string{"tsc --init"}, "pnpm exec tsc --init") {
		t.Fatal("pnpm exec should match")
	}
	if !inPlanCmd([]string{"tsc --init"}, "yarn tsc --init") {
		t.Fatal("yarn bin should match")
	}
	if inPlanCmd([]string{"tsc --init"}, "yarn install") {
		t.Fatal("yarn install is not a bin wrapper")
	}
}

func TestInPlanAndJoinAutoApprove(t *testing.T) {
	cc := &countConfirm{d: ui.Approve}
	var buf strings.Builder
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["i"],"files":["package.json"],"commands":["npm init -y","npm install express"]}`)),
		assistant(call("2", "run_shell", `{"cmd":"npm init -y && npm install express"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, cc, Config{RequirePlan: true, Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	if cc.n != 1 {
		t.Fatalf("confirm %d want 1", cc.n)
	}
	if cc.forced != 0 {
		t.Fatalf("forced %d", cc.forced)
	}
	if !strings.Contains(buf.String(), "(in plan)") {
		t.Fatalf("%q", buf.String())
	}
}

func TestApproveAllDoesNotCoverOutsidePlan(t *testing.T) {
	cc := &countConfirm{d: ui.ApproveAll}
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"]}`)),
		assistant(call("2", "write_file", `{"path":"b.txt","content":"x"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, cc, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if cc.forced != 1 {
		t.Fatalf("forced %d", cc.forced)
	}
}

func TestApproveAllCoversOutsideCommands(t *testing.T) {
	cc := &countConfirm{d: ui.ApproveAll}
	var buf strings.Builder
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"a","steps":["a"],"files":["a.txt"],"commands":["true"]}`)),
		assistant(call("2", "run_shell", `{"cmd":"mkdir extra_dir"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, cc, Config{RequirePlan: true, Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	if cc.forced != 0 {
		t.Fatalf("outside command should not force, forced=%d", cc.forced)
	}
	if !strings.Contains(buf.String(), "(outside plan, approved by a)") {
		t.Fatalf("%q", buf.String())
	}
}

func TestInPlanMultilineCommand(t *testing.T) {
	script := "cat <<'EOF'\nhello\nEOF"
	if !inPlanCmd([]string{script}, "cat <<'EOF'\nhello\nEOF") {
		t.Fatal("multiline should match")
	}
	if !inPlanCmd([]string{"cat <<'EOF'\nhello\nEOF"}, "cat <<'EOF'  hello  EOF") {
		t.Fatal("whitespace-normalized multiline")
	}
}

func TestPlanMatchTraceRules(t *testing.T) {
	var errW strings.Builder
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"skeleton","steps":["mkdir","touch"],"files":["src/a/.gitkeep","src/b/.gitkeep"]}`)),
		assistant(call("2", "run_shell", `{"cmd":"mkdir -p src/a src/b && touch src/a/.gitkeep src/b/.gitkeep"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, Trace: true, Err: &errW})
	if err != nil {
		t.Fatal(err)
	}
	s := errW.String()
	if !strings.Contains(s, "R3 mkdir") || !strings.Contains(s, "✓") {
		t.Fatalf("trace %q", s)
	}
}

func TestPlanAuthorizesLocalBinWrite(t *testing.T) {
	rel := "~/.local/bin/konsolai-agent-plan-auth-test"
	abs := filepath.Join(os.Getenv("HOME"), ".local", "bin", "konsolai-agent-plan-auth-test")
	defer os.Remove(abs)
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"bin","steps":["write"],"files":["`+rel+`"]}`)),
		assistant(call("2", "write_file", `{"path":"`+rel+`","content":"ok"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, "escape") {
				t.Fatalf("plan write escaped: %s", m.Content)
			}
		}
	}
}

func TestAgentCompleteSpinner(t *testing.T) {
	var buf bytes.Buffer
	restore := ui.TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "finish", `{"summary":"ok"}`)),
	}, nil, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "planning") || !strings.Contains(buf.String(), "\033[?25l") {
		t.Fatalf("spinner %q", buf.String())
	}
}

func TestHardExploreStopDropsTools(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "here is what I found"},
	}, nil, Config{ExploreBudget: 1, ExploreNote: ExploreNoteLookup, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.tools) < 4 {
		t.Fatalf("reqs %d", len(rep.tools))
	}
	if len(rep.tools[3]) != 0 {
		t.Fatalf("want no tools on forced call, got %d", len(rep.tools[3]))
	}
	found := false
	for _, m := range rep.reqs[3] {
		if m.Role == llm.RoleUser && m.Content == ToolsGoneNote {
			found = true
		}
	}
	if !found {
		t.Fatal("missing tools-gone note")
	}
}

func TestHardExploreStopBuildKeepsProposePlan(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		assistant(call("4", "propose_plan", `{"summary":"x","steps":["y"],"files":["a.txt"]}`)),
		assistant(call("5", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, ExploreBudget: 1, ExploreNote: ExploreNoteBuild, MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.tools) < 4 {
		t.Fatalf("reqs %d", len(rep.tools))
	}
	if len(rep.tools[3]) != 1 || rep.tools[3][0].Function.Name != "propose_plan" {
		t.Fatalf("forced build tools %+v", rep.tools[3])
	}
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(rep.toolChoices[3], &choice); err != nil || choice.Type != "function" || choice.Function.Name != "propose_plan" {
		t.Fatalf("tool_choice %s", rep.toolChoices[3])
	}
	found := false
	for _, m := range rep.reqs[3] {
		if m.Role == llm.RoleUser && m.Content == ForcedPlanNote {
			found = true
		}
	}
	if !found {
		t.Fatal("missing forced plan note")
	}
}

func TestForcedPlanDiscardsText(t *testing.T) {
	var out bytes.Buffer
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "I will now plan", ToolCalls: []llm.ToolCall{
			call("4", "propose_plan", `{"summary":"x","steps":["y"],"files":["a.txt"]}`),
		}},
		assistant(call("5", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, ExploreBudget: 1, ExploreNote: ExploreNoteBuild, MaxSteps: 8, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "I will now plan") {
		t.Fatalf("forced plan text should be discarded: %q", out.String())
	}
}

func TestMaxStepsPrintsSummary(t *testing.T) {
	var out bytes.Buffer
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "I listed twice; more remains"},
	}, nil, Config{MaxSteps: 2, Out: &out, Intent: "act"})
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(out.String(), "I listed twice") {
		t.Fatalf("%q", out.String())
	}
	if len(rep.tools) == 0 {
		t.Fatal("no requests")
	}
	last := rep.tools[len(rep.tools)-1]
	if len(last) != 0 {
		t.Fatalf("forced summary still had tools: %d", len(last))
	}
}

func TestRestoreApprovedPlan(t *testing.T) {
	msgs := []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"s","steps":["t"],"files":["a.txt"],"commands":["touch a"]}`)),
		{Role: llm.RoleTool, ToolCallID: "1", Content: "plan approved"},
		assistant(call("2", "run_shell", `{"cmd":"touch a"}`)),
	}
	files, cmds, text, ok := RestoreApprovedPlan(msgs)
	if !ok || len(files) != 1 || files[0] != "a.txt" || cmds[0] != "touch a" || !strings.Contains(text, "s") {
		t.Fatalf("%v %v %v %q", ok, files, cmds, text)
	}
}

func TestPromptNotes(t *testing.T) {
	if !strings.Contains(LookupPrompt, "never with web_fetch") {
		t.Fatal("lookup web_fetch")
	}
	if !strings.Contains(LookupPrompt, "Code analysis:") || !strings.Contains(LookupPrompt, "Observed") || !strings.Contains(LookupPrompt, "Inferred") || !strings.Contains(LookupPrompt, "Verdict") {
		t.Fatal("lookup analysis section")
	}
	if !strings.Contains(LookupPrompt, "sudo ss -ltnp") {
		t.Fatal("ss hint")
	}
	if !strings.Contains(AnswerPrompt, "Do not mention the user's project") {
		t.Fatal("answer project")
	}
	if !strings.Contains(AnswerPrompt, "If the user refers to 'this' or 'here' without content, say you will look at the current folder.") {
		t.Fatal("answer deictic fallback")
	}
	if !strings.Contains(AnswerPrompt, "About Konsolai:") || !strings.Contains(AnswerPrompt, "export PDF/HTML") {
		t.Fatal("answer about block")
	}
	if !strings.Contains(AnswerPrompt, "Never claim you cannot search or run commands") {
		t.Fatal("answer capability rule")
	}
	if ReplyInstruction("fr") != "Reply in French, the language of the user's latest message." {
		t.Fatalf("fr %q", ReplyInstruction("fr"))
	}
	if ReplyInstruction("en") != "Reply in English, the language of the user's latest message." {
		t.Fatalf("en %q", ReplyInstruction("en"))
	}
	if strings.Contains(ReplyInstruction("es"), "Spanish") {
		t.Fatalf("es leaked %q", ReplyInstruction("es"))
	}
	if ProgressLabel("working", llm.Progress{ReasoningTokens: 1200}) != "thinking · 1.2k tok" {
		t.Fatal("reasoning label")
	}
	if ProgressLabel("working", llm.Progress{ContentTokens: 300}) != "writing · 300 tok" {
		t.Fatal("content label")
	}
	for _, p := range []string{ActPrompt, CodePrompt, AnswerPrompt, LookupPrompt} {
		if !strings.Contains(p, SecretRule) {
			t.Fatal("prompt missing secret rule")
		}
		if !strings.Contains(p, FenceRule) {
			t.Fatal("prompt missing fence rule")
		}
		if !strings.Contains(p, EmojiRule) {
			t.Fatal("prompt missing emoji rule")
		}
	}
	for _, p := range []string{ActPrompt, CodePrompt, LookupPrompt} {
		if !strings.Contains(p, batchReads) {
			t.Fatal("prompt missing batch-reads rule")
		}
		if !strings.Contains(p, "pacman -Qo") || !strings.Contains(p, "Never launch GUI") || !strings.Contains(p, "Run these yourself:") {
			t.Fatal("prompt missing sys rules")
		}
		if !strings.Contains(p, "Use recall for earlier decisions, past sessions and fetched docs") {
			t.Fatal("prompt missing recall rule")
		}
	}
}

func TestAnalysisExploreNoteBeforeSources(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "finish", `{"summary":"ok"}`)),
	}, nil, Config{ExploreBudget: 1, ExploreNote: ExploreNoteLookup, MaxSteps: 6, Task: "analysis", Intent: "lookup"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, AnalysisExploreNote) {
				found = true
			}
			if m.Role == llm.RoleTool && strings.Contains(m.Content, "Answer now") {
				t.Fatal("want analysis note, got answer now")
			}
		}
	}
	if !found {
		t.Fatal("missing analysis explore note")
	}
}

func TestAnalysisDefersHardStop(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		assistant(call("4", "list_dir", `{"path":"."}`)),
		assistant(call("5", "finish", `{"summary":"ok"}`)),
	}, nil, Config{ExploreBudget: 1, ExploreNote: ExploreNoteLookup, MaxSteps: 6, Task: "analysis", Intent: "lookup"})
	if err != nil {
		t.Fatal(err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.tools) < 4 {
		t.Fatalf("reqs %d", len(rep.tools))
	}
	if len(rep.tools[3]) == 0 {
		t.Fatal("hard stop should be deferred until source files are read")
	}
}

func TestAnalysisReleasesAfterSourceReads(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package p\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	replies := []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.go"}`)),
		assistant(call("2", "read_file", `{"path":"b.go"}`)),
		assistant(call("3", "read_file", `{"path":"c.go"}`)),
		assistant(call("4", "list_dir", `{"path":"."}`)),
		assistant(call("5", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "Observed / Inferred / Verdict"},
	}
	client, rep := newReplay(t, replies)
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard,
		ExploreBudget: 3, ExploreNote: ExploreNoteLookup, MaxSteps: 8,
		Task: "analysis", Intent: "lookup",
	}, "sys", "analyse this")
	if err != nil {
		t.Fatal(err)
	}
	foundNow := false
	foundHold := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, "Answer now") {
				foundNow = true
			}
			if m.Role == llm.RoleTool && strings.Contains(m.Content, AnalysisExploreNote) {
				foundHold = true
			}
		}
	}
	if !foundNow {
		t.Fatal("after 3 source reads want answer-now note")
	}
	if foundHold {
		t.Fatal("analysis hold note after source reads")
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.tools) < 6 {
		t.Fatalf("reqs %d", len(rep.tools))
	}
	if len(rep.tools[5]) != 0 {
		t.Fatalf("want tools dropped after source reads + hard stop, got %d", len(rep.tools[5]))
	}
}

func TestAnalysisIgnoresManifestReads(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"package.json"}`)),
		assistant(call("2", "read_file", `{"path":"README.md"}`)),
		assistant(call("3", "finish", `{"summary":"ok"}`)),
	})
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard,
		ExploreBudget: 1, ExploreNote: ExploreNoteLookup, MaxSteps: 6,
		Task: "analysis", Intent: "lookup",
	}, "sys", "analyse this")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, AnalysisExploreNote) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("manifest/README reads must not release the analysis hold")
	}
}

func TestExploreBudgetCountsStepsNotCalls(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(
			call("1", "read_file", `{"path":"a.txt"}`),
			call("2", "read_file", `{"path":"b.txt"}`),
			call("3", "read_file", `{"path":"c.txt"}`),
			call("4", "read_file", `{"path":"d.txt"}`),
		),
		assistant(call("5", "finish", `{"summary":"ok"}`)),
	})
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard,
		ExploreBudget: 2, ExploreNote: ExploreNoteLookup, MaxSteps: 6,
	}, "sys", "read them")
	if err != nil {
		t.Fatal(err)
	}
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, ExploreNoteLookup) {
				t.Fatal("4 parallel reads are one step; budget 2 must not fire")
			}
		}
	}
}

func TestPrePlanExploreCountsEveryStep(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "read_file", `{"path":"b.txt"}`)),
		assistant(call("3", "run_shell", `{"cmd":"rm a.txt"}`)),
		assistant(call("4", "read_file", `{"path":"c.txt"}`)),
		assistant(call("5", "propose_plan", `{"summary":"x","steps":["y"],"files":["a.txt"]}`)),
		assistant(call("6", "finish", `{"summary":"done"}`)),
	})
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard,
		RequirePlan: true, ExploreBudget: 2, ExploreNote: ExploreNoteBuild,
		MaxSteps: 10, Intent: "build",
	}, "sys", "explore then plan")
	if err != nil {
		t.Fatal(err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.tools) < 4 {
		t.Fatalf("reqs %d", len(rep.tools))
	}
	blocked := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, "call propose_plan first") {
				blocked = true
			}
		}
	}
	if !blocked {
		t.Fatal("want gated mutating rejection")
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && strings.Contains(m.Content, ExploreNoteBuild) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing explore note; blocked run_shell must not reset the budget")
	}
}

func TestGatedMutatingDoesNotConsumeExplore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "write_file", `{"path":"a.txt","content":"x"}`)),
		assistant(call("3", "read_file", `{"path":"b.txt"}`)),
		assistant(call("4", "finish", `{"summary":"ok"}`)),
	})
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard,
		RequirePlan: true, ExploreBudget: 1, ExploreNote: ExploreNoteBuild,
		MaxSteps: 8, Intent: "build",
	}, "sys", "do not spend budget on gated writes")
	if err != nil {
		t.Fatal(err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.tools) < 4 {
		t.Fatalf("reqs %d", len(rep.tools))
	}
	if !hasSpec(rep.tools[3], "read_file") {
		t.Fatalf("gated write consumed explore; tools gone too early: %+v", specNames(rep.tools[3]))
	}
}

func TestExploreBudgetForDocs(t *testing.T) {
	if ExploreBudgetFor("lookup", "other") != 6 || ExploreBudgetFor("lookup", "analysis") != 10 {
		t.Fatal("lookup budgets")
	}
	if ExploreBudgetFor("act", "other") != 6 {
		t.Fatal("act budget")
	}
	if ExploreBudgetFor("build", "other") != 8 || ExploreBudgetFor("build", "analysis") != 12 {
		t.Fatal("build budgets")
	}
	if ExploreBudgetFor("build", "docs") != 16 {
		t.Fatal("docs budget")
	}
}

func TestDocsNudgeFiresOnce(t *testing.T) {
	var out bytes.Buffer
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"write overview","steps":["write docs/OVERVIEW.md"],"files":["docs/OVERVIEW.md"]}`)),
		{Role: llm.RoleAssistant, Content: "Here is the overview in chat."},
		assistant(call("2", "write_file", `{"path":"docs/OVERVIEW.md","content":"# Overview\n"}`)),
		{Role: llm.RoleAssistant, Content: "wrote the file"},
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, Task: "docs", Out: &out, MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	if len(rep.reqs) == 0 {
		t.Fatal("no requests")
	}
	for _, m := range rep.reqs[len(rep.reqs)-1] {
		if m.Role == llm.RoleUser && m.Content == DocsFileNote {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("nudge count %d in last request", n)
	}
	if !strings.Contains(out.String(), "wrote the file") {
		t.Fatalf("want final answer after nudge: %q", out.String())
	}
	if strings.Contains(out.String(), "Here is the overview in chat") {
		t.Fatalf("pre-nudge text should not finish: %q", out.String())
	}
}

func TestDocsExportNudge(t *testing.T) {
	var out bytes.Buffer
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"write overview","steps":["write md","export pdf"],"files":["docs/OVERVIEW.pdf"]}`)),
		{Role: llm.RoleAssistant, Content: "Here is the overview in chat."},
		assistant(call("2", "write_file", `{"path":"docs/OVERVIEW.md","content":"# Overview\n"}`)),
		{Role: llm.RoleAssistant, Content: "wrote"},
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, Task: "docs", Out: &out, MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && m.Content == DocsExportNote {
				found = true
			}
			if m.Role == llm.RoleUser && m.Content == DocsFileNote {
				t.Fatal("want export_doc nudge, got generic docs note")
			}
		}
	}
	if !found {
		t.Fatal("missing export_doc nudge")
	}
}

func TestLeanResumeElidesOldToolOutputs(t *testing.T) {
	longArgs := `{"path":"` + strings.Repeat("x", 300) + `"}`
	hist := []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("1", "read_file", longArgs)}},
		{Role: llm.RoleTool, Content: "huge old output from read_file", ToolCallID: "1"},
		{Role: llm.RoleAssistant, Content: "done first"},
		{Role: llm.RoleUser, Content: "second"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("2", "read_file", `{"path":"b.txt"}`)}},
		{Role: llm.RoleTool, Content: "recent output", ToolCallID: "2"},
	}
	got := LeanResume(hist, 0.30)
	if !toolProtocolValid(got) {
		t.Fatal("protocol broken")
	}
	var old, recent bool
	for _, m := range got {
		if m.Role == llm.RoleTool && m.ToolCallID == "1" {
			if m.Content != resumeToolElided {
				t.Fatalf("old tool %q", m.Content)
			}
			old = true
		}
		if m.Role == llm.RoleTool && m.ToolCallID == "2" {
			if m.Content != "recent output" {
				t.Fatalf("recent tool %q", m.Content)
			}
			recent = true
		}
		for _, c := range m.ToolCalls {
			if len(c.Function.Arguments) > 200 {
				t.Fatalf("args len %d", len(c.Function.Arguments))
			}
		}
	}
	if !old || !recent {
		t.Fatalf("old=%v recent=%v", old, recent)
	}
	if hist[1].ToolCalls[0].Function.Arguments != longArgs {
		t.Fatal("LeanResume must not mutate the input")
	}
}

func TestLeanResumeCapsPairs(t *testing.T) {
	var hist []llm.Message
	for i := 0; i < 12; i++ {
		hist = append(hist,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("user-%d ", i) + strings.Repeat("u", 2500)},
			llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("asst-%d ", i) + strings.Repeat("a", 2500)},
		)
	}
	got := LeanResume(hist, 0.30)
	if resumeTokens(got, 0.30) > resumeTokenCap {
		t.Fatalf("tokens %d", resumeTokens(got, 0.30))
	}
	if len(got) == 0 || got[0].Role != llm.RoleUser {
		t.Fatalf("pair broken: first %+v", got)
	}
	for i := 0; i < len(got)-1; i++ {
		if got[i].Role == llm.RoleUser && got[i+1].Role != llm.RoleAssistant {
			t.Fatalf("user not followed by assistant at %d", i)
		}
	}
	last := got[len(got)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, "asst-11") {
		t.Fatalf("dropped the newest pair: %+v", last)
	}
	if strings.Contains(got[0].Content, "user-0") {
		t.Fatal("oldest pair should have been dropped")
	}
}

func TestLeanResumeKeepsToolProtocol(t *testing.T) {
	var hist []llm.Message
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("c%d", i)
		hist = append(hist,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("q%d %s", i, strings.Repeat("q", 2000))},
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call(id, "read_file", `{"path":"f.txt"}`)}},
			llm.Message{Role: llm.RoleTool, Content: strings.Repeat("out", 800), ToolCallID: id},
		)
	}
	got := LeanResume(hist, 0.30)
	if !toolProtocolValid(got) {
		t.Fatalf("invalid protocol after cap: %d msgs", len(got))
	}
	for i, m := range got {
		if m.Role != llm.RoleTool {
			continue
		}
		if i == 0 || got[i-1].Role != llm.RoleAssistant {
			t.Fatalf("tool %d not after assistant", i)
		}
		found := false
		for _, c := range got[i-1].ToolCalls {
			if c.ID == m.ToolCallID {
				found = true
			}
		}
		if !found {
			t.Fatalf("tool %s has no matching tool_call", m.ToolCallID)
		}
	}
}

func TestTraceDumpWritesRedactedJSON(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	huge := `{"path":"docs/OVERVIEW.pdf","format":"pdf","content":"` + strings.Repeat("hello world ", 300) + `"}`
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "export_doc", huge)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, nil, Config{Trace: true, SessionID: "sess1", MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(state, "konsolai", "trace", "sess1-1.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if !strings.Contains(string(b), "export_doc") || !strings.Contains(string(b), `"content"`) {
		t.Fatalf("dump %s", b)
	}
	if len(huge) < 3000 {
		t.Fatalf("fixture too small: %d", len(huge))
	}
}

func TestNarrationTruncatedInHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	long := strings.Repeat("word ", 200)
	if utf8.RuneCountInString(long) <= 500 {
		t.Fatalf("need >500 runes, got %d", utf8.RuneCountInString(long))
	}
	_, rep, err := setupCfg(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: long, ToolCalls: []llm.ToolCall{call("1", "list_dir", `{"path":"."}`)}},
		{Role: llm.RoleAssistant, Content: "done"},
	}, nil, Config{MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.reqs) < 2 {
		t.Fatalf("reqs %d", len(rep.reqs))
	}
	var got string
	for _, m := range rep.reqs[1] {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			got = m.Content
		}
	}
	if utf8.RuneCountInString(got) != 300 {
		t.Fatalf("want 300 runes, got %d %q", utf8.RuneCountInString(got), got)
	}
}

func TestReadDedupeSkipsCoveredRange(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	body := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "read_file", `{"path":"a.txt","start_line":2,"end_line":4}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: &out, MaxSteps: 6,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "read a.txt (already shown, skipped)") {
		t.Fatalf("display %q", out.String())
	}
	var skip string
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && m.ToolCallID == "2" {
				skip = m.Content
			}
		}
	}
	if !strings.Contains(skip, "already in context above (step 1, lines 1-5)") {
		t.Fatalf("skip %q", skip)
	}
}

func TestReadDedupeRerunsAfterChange(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "write_file", `{"path":"a.txt","content":"new\n"}`)),
		assistant(call("3", "read_file", `{"path":"a.txt"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, stubConfirm{d: ui.Approve}, Config{MaxSteps: 8, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil)})
	if err != nil {
		t.Fatal(err)
	}
	var second string
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && m.ToolCallID == "3" {
				second = m.Content
			}
		}
	}
	if strings.Contains(second, "already in context") {
		t.Fatalf("changed file was skipped: %q", second)
	}
	if !strings.Contains(second, "new") {
		t.Fatalf("want re-read, got %q", second)
	}
}

func TestReadDedupeInvalidatedByElision(t *testing.T) {
	spans := []readSpan{{abs: "/a", hash: "h", start: 1, end: 10, step: 1, toolID: "t1"}}
	msgs := []llm.Message{{Role: llm.RoleTool, Content: elidedMarker("read_file", "a"), ToolCallID: "t1"}}
	got := purgeElidedReads(spans, msgs)
	if len(got) != 0 {
		t.Fatalf("want purged, got %+v", got)
	}
	if _, ok := findReadSpan(spans, "/a", "h", 2, 5); !ok {
		t.Fatal("subset should be covered before purge")
	}
}

func padAfter(msgs []llm.Message) []llm.Message {
	for i := 0; i < protectLastMsgs; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("pad%d", i)})
	}
	return msgs
}

func TestElisionDropsDedupeRecords(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\nfunc F() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	msgs := padAfter([]llm.Message{
		assistant(call("t1", "read_file", `{"path":"a.go"}`)),
		{Role: llm.RoleTool, Content: strings.Repeat("body\n", 40), ToolCallID: "t1"},
	})
	spans := []readSpan{{abs: p, hash: "h", start: 1, end: 10, step: 1, toolID: "t1"}}
	key := "read_file\x00" + canonicalJSON(`{"path":"a.go"}`)
	seen := map[string]struct{}{key: {}}
	if !elideOneTool(msgs, &spans, seen) {
		t.Fatal("want elide")
	}
	if len(spans) != 0 {
		t.Fatalf("span survived %+v", spans)
	}
	if _, ok := seen[key]; ok {
		t.Fatal("seen survived")
	}
	if !isElided(msgs[1].Content) {
		t.Fatalf("not elided %q", msgs[1].Content)
	}
	for _, sp := range spans {
		if !spanVisible(sp, msgs) {
			t.Fatalf("dedupe refers to elided %+v", sp)
		}
	}
}

func TestElideReadPlaceholderOutline(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.go")
	body := "package main\nfunc Hello() {}\nfunc World() {}\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	a, ok := fileElidePlaceholder(p, "main.go")
	if !ok {
		t.Fatal("want placeholder")
	}
	b, _ := fileElidePlaceholder(p, "main.go")
	if a != b {
		t.Fatal("not deterministic")
	}
	if !strings.Contains(a, "[elided] main.go") || !strings.Contains(a, "Outline:") {
		t.Fatalf("%q", a)
	}
	if !strings.Contains(a, "Hello") || !strings.Contains(a, "Re-read only the specific line ranges") {
		t.Fatalf("%q", a)
	}
	huge := dir + "/big.go"
	var buf strings.Builder
	buf.WriteString("package p\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&buf, "func F%d() {}\n", i)
	}
	if err := os.WriteFile(huge, []byte(buf.String()), 0644); err != nil {
		t.Fatal(err)
	}
	got, ok := fileElidePlaceholder(huge, "big.go")
	if !ok {
		t.Fatal("want huge placeholder")
	}
	idx := strings.Index(got, "Outline:\n")
	if idx < 0 {
		t.Fatal(got)
	}
	rest := strings.TrimPrefix(got[idx+len("Outline:\n"):], "")
	end := strings.Index(rest, "\nRe-read")
	if end < 0 {
		t.Fatal(got)
	}
	if len(rest[:end]) > elideOutlineCap {
		t.Fatalf("outline %d > %d", len(rest[:end]), elideOutlineCap)
	}
}

func TestPostPlanNoteAtFour(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"s","steps":["write"],"files":["out.md"]}`)),
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		assistant(call("4", "read_file", `{"path":"a.txt"}`)),
		assistant(call("5", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, Intent: "build",
		Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil), MaxSteps: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && m.Content == PostPlanNote {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing post-plan note at 4")
	}
}

func TestPostPlanForceAtSix(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"s","steps":["write"],"files":["out.md"]}`)),
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		assistant(call("4", "read_file", `{"path":"a.txt"}`)),
		assistant(call("5", "list_dir", `{"path":"."}`)),
		assistant(call("6", "list_dir", `{"path":"."}`)),
		assistant(call("7", "write_file", `{"path":"out.md","content":"# x\n"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, Intent: "build",
		Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil), MaxSteps: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	forced := false
	for i, specs := range rep.tools {
		if string(rep.toolChoices[i]) == `"required"` {
			if hasSpec(specs, "read_file") || hasSpec(specs, "outline") || hasSpec(specs, "propose_plan") {
				t.Fatalf("forced call still has readonly/plan: %v", specNames(specs))
			}
			if !hasSpec(specs, "write_file") || !hasSpec(specs, "finish") {
				t.Fatalf("forced call missing mutating tools: %v", specNames(specs))
			}
			forced = true
		}
	}
	if !forced {
		t.Fatalf("want tool_choice required after 6 readonly, choices=%v", rep.toolChoices)
	}
}

func TestPostPlanResetOnMutation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"s","steps":["write"],"files":["out.md"]}`)),
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("w", "write_file", `{"path":"out.md","content":"# x\n"}`)),
		assistant(call("3", "read_file", `{"path":"a.txt"}`)),
		assistant(call("4", "list_dir", `{"path":"."}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, Intent: "build",
		Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil), MaxSteps: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && m.Content == PostPlanNote {
				t.Fatal("note should reset after write_file")
			}
		}
	}
}

func TestDocsExportNoteAfterWrite(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"s","steps":["write","export"],"files":["docs/OVERVIEW.md","docs/OVERVIEW.pdf"]}`)),
		assistant(call("w", "write_file", `{"path":"docs/OVERVIEW.md","content":"# x\n"}`)),
		assistant(call("1", "read_file", `{"path":"docs/OVERVIEW.md"}`)),
		assistant(call("2", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, Intent: "build", Task: "docs",
		Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil), MaxSteps: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && m.Content == DocsExportNote {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing export_doc note after write")
	}
}

func TestDocsExportForceAfterWrite(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"s","steps":["write","export"],"files":["docs/OVERVIEW.md","docs/OVERVIEW.pdf"]}`)),
		assistant(call("w", "write_file", `{"path":"docs/OVERVIEW.md","content":"# x\n"}`)),
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "list_dir", `{"path":"docs"}`)),
		assistant(call("3", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, Intent: "build", Task: "docs",
		Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil), MaxSteps: 14,
	})
	if err != nil {
		t.Fatal(err)
	}
	forced := false
	for i, specs := range rep.tools {
		var choice struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(rep.toolChoices[i], &choice); err != nil || choice.Function.Name != "export_doc" {
			continue
		}
		if hasSpec(specs, "read_file") || hasSpec(specs, "run_shell") || hasSpec(specs, "write_file") {
			t.Fatalf("forced export still has extra tools: %v", specNames(specs))
		}
		if !hasSpec(specs, "export_doc") {
			t.Fatalf("forced export missing export_doc: %v", specNames(specs))
		}
		forced = true
	}
	if !forced {
		t.Fatalf("want tool_choice export_doc after 3 post-write steps, choices=%v", rep.toolChoices)
	}
}

func TestPostPlanDocsFileNote(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"s","steps":["write"],"files":["docs/OVERVIEW.md","docs/OVERVIEW.pdf"]}`)),
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "list_dir", `{"path":"."}`)),
		assistant(call("3", "list_dir", `{"path":"."}`)),
		assistant(call("4", "finish", `{"summary":"done"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, Intent: "build", Task: "docs",
		Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil), MaxSteps: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.Contains(m.Content, "docs/OVERVIEW.md") && strings.Contains(m.Content, "write_file") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("missing docs file note")
	}
}

func hasSpec(specs []llm.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Function.Name == name {
			return true
		}
	}
	return false
}

func specNames(specs []llm.ToolSpec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.Function.Name)
	}
	return out
}

type seqConfirm struct {
	ds []ui.Decision
	i  int
}

func (s *seqConfirm) next() ui.Decision {
	d := s.ds[s.i]
	if s.i < len(s.ds)-1 {
		s.i++
	}
	return d
}

func (s *seqConfirm) Confirm(string, string) (ui.Decision, string, error) {
	return s.next(), "no", nil
}
func (s *seqConfirm) ConfirmForced(string, string) (ui.Decision, string, error) {
	return s.next(), "no", nil
}

func TestRejectedPlanThinksOnNext(t *testing.T) {
	conf := &seqConfirm{ds: []ui.Decision{ui.Reject, ui.Approve}}
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "list_dir", `{"path":"."}`)),
		assistant(call("2", "propose_plan", `{"summary":"a","steps":["w"],"files":["a.txt"]}`)),
		assistant(call("2b", "propose_plan", `{"summary":"a","steps":["w"],"files":["a.txt"]}`)),
		assistant(call("3", "propose_plan", `{"summary":"a","steps":["w"],"files":["a.txt"]}`)),
		assistant(call("4", "finish", `{"summary":"done"}`)),
	}, conf, Config{RequirePlan: true, ThinkingPolicy: ThinkPlanStep, MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, false, true, true, false}
	if len(rep.think) != len(want) {
		t.Fatalf("think %v want %v", rep.think, want)
	}
	for i, g := range rep.think {
		if g != want[i] {
			t.Fatalf("think[%d]=%v want %v (%v)", i, g, want[i], rep.think)
		}
	}
}

func TestPlanStepInvalidPlanNoRetry(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"x"}`)),
		assistant(call("2", "finish", `{"summary":"stopped"}`)),
	}, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, AutoApprove: true, ThinkingPolicy: ThinkPlanStep, MaxSteps: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.think) != 2 || rep.think[0] || rep.think[1] {
		t.Fatalf("think %v", rep.think)
	}
	got := ""
	for _, msgs := range rep.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && m.ToolCallID == "1" {
				got = m.Content
			}
		}
	}
	if !strings.Contains(got, "error:") {
		t.Fatalf("want prepare error, got %q", got)
	}
}

func TestExportDocUnknownContentRejected(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	args := `{"path":"out.html","format":"html","content":"` + strings.Repeat("x", 800) + `"}`
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "export_doc", args)),
		{Role: llm.RoleAssistant, Content: "ok"},
	}, nil, Config{MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "unknown field") {
		t.Fatalf("want unknown field reject, got %q", got.Content)
	}
}

func TestFinishPicksLongerNarration(t *testing.T) {
	long := "This narration is clearly longer than the finish summary."
	var out bytes.Buffer
	_, _, err := setupCfg(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: long, ToolCalls: []llm.ToolCall{call("1", "finish", `{"summary":"done"}`)}},
	}, nil, Config{Out: &out, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, long) {
		t.Fatalf("want narration %q", s)
	}
	if strings.Contains(s, "done") {
		t.Fatalf("short summary should not print %q", s)
	}
}

func TestFinishPicksLongerSummary(t *testing.T) {
	sum := "Finished the requested work successfully."
	var out bytes.Buffer
	_, _, err := setupCfg(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: "OK.", ToolCalls: []llm.ToolCall{call("1", "finish", `{"summary":"`+sum+`"}`)}},
	}, nil, Config{Out: &out, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, sum) {
		t.Fatalf("want summary %q", s)
	}
	if strings.Contains(s, "OK.") {
		t.Fatalf("short narration should not print %q", s)
	}
}

func TestOnShellRemembersOutput(t *testing.T) {
	var got string
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "run_shell", `{"cmd":"echo captured"}`)),
		assistant(call("2", "finish", `{"summary":"ok"}`)),
	}, nil, Config{OnShell: func(s string) { got = s }, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "captured") {
		t.Fatalf("%q", got)
	}
}

func TestIdenticalPlanRejectStops(t *testing.T) {
	prose := "Read-only health check of the repo: build, vet, and test status."
	replies := make([]llm.Message, 0, 16)
	for i := 0; i < 15; i++ {
		replies = append(replies, assistant(call(fmt.Sprintf("p%d", i), "propose_plan", prose)))
	}
	replies = append(replies, assistant(call("fin", "finish", `{"summary":"should not run"}`)))
	var out bytes.Buffer
	_, rep, err := setupCfg(t, replies, stubConfirm{d: ui.Approve}, Config{
		RequirePlan: true, MaxSteps: 15, Out: &out,
	})
	if errors.Is(err, ErrMaxSteps) {
		t.Fatal("identical rejects ran to the step cap")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.reqs) != 2 {
		t.Fatalf("model calls %d, want 2", len(rep.reqs))
	}
	s := out.String()
	for _, want := range []string{"go build ./...", "go vet ./...", "go test ./...", "files may be omitted: yes"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %q", want, s)
		}
	}
}

func TestShellStillRequiresPlan(t *testing.T) {
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "run_shell", `{"cmd":"go build ./..."}`)),
		{Role: llm.RoleAssistant, Content: "blocked"},
	}, nil, Config{RequirePlan: true, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "call propose_plan first") {
		t.Fatalf("%q", got.Content)
	}
}

func TestAcceptedPlanAllowsShell(t *testing.T) {
	plan := `{"summary":"health","steps":["go build ./...","go vet ./...","go test ./..."],"commands":["go build ./...","go vet ./...","go test ./..."]}`
	_, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "propose_plan", plan)),
		assistant(call("2", "run_shell", `{"cmd":"go build ./..."}`)),
		assistant(call("3", "finish", `{"summary":"built"}`)),
	}, stubConfirm{d: ui.Approve}, Config{RequirePlan: true, MaxSteps: 6})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.reqs) < 3 {
		t.Fatalf("reqs %d", len(rep.reqs))
	}
	var shell string
	for _, m := range rep.reqs[2] {
		if m.ToolCallID == "2" {
			shell = m.Content
		}
	}
	if shell == "" {
		t.Fatal("shell did not run")
	}
	if strings.Contains(shell, "call propose_plan first") {
		t.Fatalf("%q", shell)
	}
}
