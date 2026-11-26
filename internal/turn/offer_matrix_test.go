package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/tools"
)

const (
	matrixBase = "edit_file export_doc find_files finish list_dir outline propose_plan read_file remember run_shell search web_fetch write_file"
	matrixPlan = "find_files list_dir outline propose_plan read_file web_fetch"
	matrixLook = "find_files finish list_dir outline read_file run_shell search web_fetch"
)

type matrixRecorder struct {
	mu   sync.Mutex
	seen [][]string
	reps []llm.Message
	i    int
}

func (m *matrixRecorder) client(t *testing.T) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"test","max_model_len":8192}]}`))
			return
		}
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		var names []string
		for _, tl := range body.Tools {
			names = append(names, tl.Function.Name)
		}
		sort.Strings(names)
		m.mu.Lock()
		m.seen = append(m.seen, names)
		i := m.i
		if m.i < len(m.reps)-1 {
			m.i++
		}
		m.mu.Unlock()
		msg := m.reps[i]
		if len(msg.ToolCalls) == 0 && msg.Content != "" {
			w.Header().Set("Content-Type", "text/event-stream")
			payload, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": msg.Content}}}})
			_, _ = io.WriteString(w, "data: "+string(payload)+"\n\ndata: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": msg}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "test")
	c.PinContext(8192)
	c.SetRetry(0, 0)
	return c
}

func matrixCall(id, name, args string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}}}
}

func matrixRun(t *testing.T, rec *matrixRecorder, req Request, mcpBridge *tools.MCPBridge, mcpProfile config.Profile) [][]string {
	t.Helper()
	r := testRunner(t, rec.client(t), autoConfirm{})
	if mcpBridge != nil {
		live := r.Profile
		live.MCP = mcpProfile.MCP
		r.Profile = live
		r.MCPRuntime = &MCPRuntime{Profile: live, Bridge: mcpBridge, MaxInline: 15}
	}
	_ = r.RunTurn(context.Background(), req)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := make([][]string, len(rec.seen))
	for i := range rec.seen {
		out[i] = rec.seen[i]
	}
	return out
}

func sortedJoin(s string) string {
	fields := strings.Fields(s)
	sort.Strings(fields)
	return strings.Join(fields, " ")
}

func namesOf(mcpNames ...string) string {
	all := strings.Fields(matrixBase)
	all = append(all, mcpNames...)
	sort.Strings(all)
	return strings.Join(all, " ")
}

func assertLists(t *testing.T, label string, got [][]string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d requests, want %d: %v", label, len(got), len(want), got)
	}
	for i := range want {
		if strings.Join(got[i], " ") != want[i] {
			t.Fatalf("%s request %d:\n got %q\nwant %q", label, i, strings.Join(got[i], " "), want[i])
		}
	}
}

type matrixMCP struct{}

func (matrixMCP) Tools(_ context.Context, server string) ([]mcp.Tool, error) {
	return []mcp.Tool{
		{Name: "read", Description: "read", ReadOnly: true},
		{Name: "mut", Description: "mut", ReadOnly: false},
	}, nil
}
func (matrixMCP) Call(context.Context, string, string, map[string]any) (mcp.Result, error) {
	return mcp.Result{}, nil
}

func matrixProfile() config.Profile {
	p := config.Profile{Name: "default", Memory: config.Memory{SessionIdleMinutes: 30}, Agent: config.Agent{BuildThinking: "plan_step"}}
	return p
}

func matrixBridge(trust string) (*tools.MCPBridge, config.Profile) {
	profile := matrixProfile()
	s := config.DefaultMCPServer()
	s.Transport = "stdio"
	s.Command = "fake"
	s.Trust = trust
	profile.MCP.Servers = map[string]config.MCPServer{"big": s}
	return tools.NewMCPBridge(matrixMCP{}, profile), profile
}

func TestOfferedToolMatrix(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		reps []llm.Message
		req  Request
		want []string
	}{
		{"answer", []llm.Message{{Role: llm.RoleAssistant, Content: "hi"}}, Request{Text: "hi", Intent: "answer", Yes: true, Cwd: dir, New: true}, []string{"escalate"}},
		{"lookup", []llm.Message{matrixCall("1", "finish", `{"summary":"done"}`)}, Request{Text: "what files", Intent: "lookup", Yes: true, Cwd: dir, New: true}, []string{matrixLook}},
		{"act low", []llm.Message{matrixCall("1", "write_file", `{"path":"a.txt","content":"hi"}`), matrixCall("2", "finish", `{"summary":"wrote"}`)}, Request{Text: "write a.txt", Intent: "act", Yes: true, Cwd: dir, New: true}, []string{namesOf(), namesOf()}},
		{"build", []llm.Message{matrixCall("1", "propose_plan", `{"summary":"s","steps":["a"],"commands":["go test ./..."]}`), matrixCall("2", "finish", `{"summary":"done"}`)}, Request{Text: "build it", Intent: "build", Yes: true, Cwd: dir, New: true}, []string{matrixPlan, namesOf()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matrixRun(t, &matrixRecorder{reps: tc.reps}, tc.req, nil, config.Profile{})
			assertLists(t, tc.name, got, tc.want...)
		})
	}
}

func TestOfferedToolMatrixActHigh(t *testing.T) {
	dir := t.TempDir()
	rec := &matrixRecorder{reps: []llm.Message{
		{Role: llm.RoleAssistant, Content: `{"intent":"act","language":"en","reason":"multi","reasoning":true,"effort":"high","task":"other","continues_session":0}`},
		matrixCall("1", "write_file", `{"path":"a.txt","content":"no"}`),
		{Role: llm.RoleAssistant, Content: "blocked"},
	}}
	seen := matrixRun(t, rec, Request{Text: "set up several files", Yes: true, Cwd: dir, New: true}, nil, config.Profile{})
	if len(seen) < 2 {
		t.Fatalf("seen %v", seen)
	}
	if strings.Join(seen[1], " ") != matrixPlan {
		t.Fatalf("pre-plan act high tools %q, want %q", strings.Join(seen[1], " "), matrixPlan)
	}
	for _, n := range seen[1] {
		if n == "write_file" || n == "run_shell" {
			t.Fatalf("act high pre-plan offered mutating tool %q", n)
		}
	}
}

func TestOfferedToolMatrixWithMCP(t *testing.T) {
	dir := t.TempDir()
	bridge, bridgeProfile := matrixBridge("auto")
	withMCP := namesOf("big__mut", "big__read")
	lookMCP := sortedJoin(matrixLook + " big__read")
	cases := []struct {
		name string
		reps []llm.Message
		req  Request
		want []string
	}{
		{"lookup", []llm.Message{matrixCall("1", "finish", `{"summary":"done"}`)}, Request{Text: "what files", Intent: "lookup", Yes: true, Cwd: dir, New: true}, []string{lookMCP}},
		{"act low", []llm.Message{matrixCall("1", "write_file", `{"path":"a.txt","content":"hi"}`), matrixCall("2", "finish", `{"summary":"wrote"}`)}, Request{Text: "write a.txt", Intent: "act", Yes: true, Cwd: dir, New: true}, []string{withMCP, withMCP}},
		{"build", []llm.Message{matrixCall("1", "propose_plan", `{"summary":"s","steps":["a"],"commands":["go test ./..."]}`), matrixCall("2", "finish", `{"summary":"done"}`)}, Request{Text: "build it", Intent: "build", Yes: true, Cwd: dir, New: true}, []string{matrixPlan, withMCP}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matrixRun(t, &matrixRecorder{reps: tc.reps}, tc.req, bridge, bridgeProfile)
			assertLists(t, "mcp "+tc.name, got, tc.want...)
		})
	}
}

func TestOfferedToolMatrixConfigProtection(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, ".real", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("defaults: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_CONFIG", real)
	rec := &matrixRecorder{reps: []llm.Message{matrixCall("1", "write_file", `{"path":"a.txt","content":"hi"}`), matrixCall("2", "finish", `{"summary":"wrote"}`)}}
	seen := matrixRun(t, rec, Request{Text: "write a.txt", Intent: "act", Yes: true, Cwd: t.TempDir(), New: true}, nil, config.Profile{})
	if len(seen) != 2 || strings.Join(seen[0], " ") != namesOf() || strings.Join(seen[1], " ") != namesOf() {
		t.Fatalf("config protection changed the offered act tools: %v", seen)
	}
	rec = &matrixRecorder{reps: []llm.Message{matrixCall("1", "propose_plan", `{"summary":"s","steps":["a"],"commands":["go test ./..."]}`), matrixCall("2", "finish", `{"summary":"done"}`)}}
	seen = matrixRun(t, rec, Request{Text: "build it", Intent: "build", Yes: true, Cwd: t.TempDir(), New: true}, nil, config.Profile{})
	assertLists(t, "config protection build", seen, matrixPlan, namesOf())
}

var _ = fmt.Sprintf
