package agent

import (
	"context"
	"encoding/json"
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

// mcpMutableTool is a bridge-shaped mutating tool used by the plan-matching
// tests: exposed name mcp_mut, identity fake:tool.
type mcpMutableTool struct{}

func (mcpMutableTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Type: "function", Function: llm.FunctionSpec{
		Name: "mcp_mut", Description: "fake mcp", Parameters: json.RawMessage(`{"type":"object"}`),
	}}
}
func (mcpMutableTool) Prepare(string) (tools.Action, error) { return mcpMutableAction{}, nil }

type mcpMutableAction struct{}

func (mcpMutableAction) Summary() string        { return "mcp fake:tool" }
func (mcpMutableAction) Preview() string        { return "mcp fake:tool" }
func (mcpMutableAction) Kind() tools.ActionKind { return tools.Mutating }
func (mcpMutableAction) MCPServer() string      { return "fake" }
func (mcpMutableAction) MCPTool() string        { return "tool" }
func (mcpMutableAction) Run(context.Context) (tools.Result, error) {
	return tools.Result{Output: "mcp result"}, nil
}

type countingConfirm struct {
	calls int
	d     ui.Decision
}

func (c *countingConfirm) Confirm(string, string) (ui.Decision, string, error) {
	c.calls++
	return c.d, "", nil
}
func (c *countingConfirm) ConfirmForced(s, p string) (ui.Decision, string, error) {
	return c.Confirm(s, p)
}

func mcpPlanServer(t *testing.T, planTools, then string) *llm.Client {
	t.Helper()
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"propose_plan","arguments":` + strconvJSON(`{"summary":"s","steps":["a"],"commands":["go test ./..."],"tools":[`+planTools+`]}`) + `}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"` + then + `","arguments":"{}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":16384}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(replies[hit]))
		if hit < len(replies)-1 {
			hit++
		}
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "m")
	c.PinContext(16384)
	c.SetRetry(0, 0)
	return c
}

func strconvJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func runMCPPlan(t *testing.T, client *llm.Client, conf *countingConfirm) string {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(g, io.Discard, nil, nil, nil).Add(mcpMutableTool{})
	var out strings.Builder
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: conf, Out: &out, Err: io.Discard,
		RequirePlan: true, MaxSteps: 8, Intent: "build",
	}, "sys", "change a file"); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String()
}

func TestMCPToolDeclaredInPlanIsAccepted(t *testing.T) {
	conf := &countingConfirm{d: ui.Approve}
	out := runMCPPlan(t, mcpPlanServer(t, `"fake:tool"`, "mcp_mut"), conf)
	if !strings.Contains(out, "(in plan)") {
		t.Fatalf("declared MCP tool was not treated as in plan:\n%s", out)
	}
	if strings.Contains(out, "outside plan") {
		t.Fatalf("declared MCP tool marked outside plan:\n%s", out)
	}
	if conf.calls != 1 {
		t.Fatalf("want only the plan confirmation, got %d", conf.calls)
	}
}

func TestMCPToolOutsidePlanAsksAgain(t *testing.T) {
	conf := &countingConfirm{d: ui.Approve}
	out := runMCPPlan(t, mcpPlanServer(t, `"other:tool"`, "mcp_mut"), conf)
	if !strings.Contains(out, "outside plan") {
		t.Fatalf("undeclared MCP tool was not marked outside plan:\n%s", out)
	}
	if conf.calls < 2 {
		t.Fatalf("outside-plan MCP call did not ask again: %d confirmations", conf.calls)
	}
}

func TestUnknownMCPManagementToolIsRefused(t *testing.T) {
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"mcp_add_server","arguments":"{}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":8192}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(replies[hit]))
		if hit < len(replies)-1 {
			hit++
		}
	}))
	defer srv.Close()
	client := llm.New(srv.URL+"/v1", "m")
	client.PinContext(8192)
	client.SetRetry(0, 0)
	root := t.TempDir()
	g, _ := safety.New(root)
	var out strings.Builder
	if err := Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil).Add(mcpMutableTool{}),
		Confirmer: stubConfirm{d: ui.Approve}, Out: &out, Err: io.Discard,
		MaxSteps: 4, Intent: "act",
	}, "sys", "add an mcp server"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unknown tool") {
		t.Fatalf("a server-management call was not refused as unknown:\n%s", out.String())
	}
}

// TestMCPToolHistoryPassesStrictProvider rejects a request whose MCP tool
// history does not pair every assistant call with a tool reply.
func TestMCPToolHistoryPassesStrictProvider(t *testing.T) {
	var mu sync.Mutex
	var violations []string
	reply := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":8192}]}`))
			return
		}
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		pending := map[string]bool{}
		for _, m := range body.Messages {
			switch m.Role {
			case "assistant":
				if len(pending) > 0 {
					violations = append(violations, "assistant tool_calls left unanswered")
				}
				for _, c := range m.ToolCalls {
					pending[c.ID] = true
				}
			case "tool":
				if pending[m.ToolCallID] {
					delete(pending, m.ToolCallID)
				}
			}
		}
		if len(pending) > 0 {
			violations = append(violations, "missing tool reply")
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if reply == 0 {
			reply++
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"mcp_mut","arguments":"{}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer srv.Close()
	client := llm.New(srv.URL+"/v1", "m")
	client.PinContext(8192)
	client.SetRetry(0, 0)
	root := t.TempDir()
	g, _ := safety.New(root)
	if err := Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil).Add(mcpMutableTool{}),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, Err: io.Discard,
		MaxSteps: 4, Intent: "act",
	}, "sys", "do it"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(violations) > 0 {
		t.Fatalf("strict provider saw invalid tool history: %v", violations)
	}
}
