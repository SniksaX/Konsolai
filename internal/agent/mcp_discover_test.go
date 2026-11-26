package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

type discoverCaller struct {
	tools map[string][]mcp.Tool
}

func (d *discoverCaller) Tools(_ context.Context, server string) ([]mcp.Tool, error) {
	return d.tools[server], nil
}

func (d *discoverCaller) Call(_ context.Context, server, tool string, _ map[string]any) (mcp.Result, error) {
	return mcp.Result{Content: []mcp.Content{{Type: "text", Text: server + ":" + tool + " ok"}}}, nil
}

func discoverCall(name string, readOnly bool) mcp.Tool {
	return mcp.Tool{Name: name, Description: "discovered " + name, ReadOnly: readOnly,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"n":{"type":"string"}}}`)}
}

func discoverProfile(trust string) config.Config {
	p := config.Builtin()
	p.MCP.MaxInlineTools = 1
	s := config.DefaultMCPServer()
	s.Transport = "stdio"
	s.Command = "fake"
	s.Trust = trust
	p.MCP.Servers = map[string]config.MCPServer{"big": s}
	return p
}

// discoverRegistry builds the turn-shaped registry for the test: the ordinary
// tools plus only the find meta-tool (the catalog is above the inline cap).
func discoverRegistry(t *testing.T, trust string) (*tools.Registry, *discoverCaller) {
	t.Helper()
	caller := &discoverCaller{tools: map[string][]mcp.Tool{"big": {
		discoverCall("danger", false),
		discoverCall("read", true),
	}}}
	bridge := tools.NewMCPBridge(caller, discoverProfile(trust))
	exposed, err := bridge.Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(g, io.Discard, nil, nil, nil).Add(bridge.FindToolFor(exposed))
	return reg, caller
}

func toolCallJSON(id, name, args string) string {
	b, _ := json.Marshal(args)
	return fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}`, id, name, string(b))
}

type replyStep struct {
	content string
}

// strictDiscoverProvider validates tool history pairing and rejects a call
// whose schema was not offered in the request that produced it.
func strictDiscoverProvider(t *testing.T, replies []string, strictCalls bool) (*llm.Client, *[]string, *[]int) {
	t.Helper()
	var mu sync.Mutex
	toolsSeen := &[]string{}
	violations := &[]int{}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":16384}]}`))
			return
		}
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			RawMessages []json.RawMessage `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		allowed := map[string]bool{}
		for _, tl := range body.Tools {
			allowed[tl.Function.Name] = true
		}
		mu.Lock()
		*toolsSeen = append(*toolsSeen, strings.Join(sortedKeys(allowed), ","))
		// Validate history pairing from the raw messages.
		var msgs []struct {
			Role       string            `json:"role"`
			ToolCallID string            `json:"tool_call_id"`
			ToolCalls  []json.RawMessage `json:"tool_calls"`
		}
		for _, rm := range body.RawMessages {
			var m struct {
				Role       string            `json:"role"`
				ToolCallID string            `json:"tool_call_id"`
				ToolCalls  []json.RawMessage `json:"tool_calls"`
			}
			_ = json.Unmarshal(rm, &m)
			msgs = append(msgs, m)
		}
		pending := map[string]bool{}
		bad := false
		for _, m := range msgs {
			switch m.Role {
			case "assistant":
				if len(pending) > 0 {
					bad = true
				}
				for _, tc := range m.ToolCalls {
					var c struct {
						ID string `json:"id"`
					}
					_ = json.Unmarshal(tc, &c)
					pending[c.ID] = true
				}
			case "tool":
				if !pending[m.ToolCallID] {
					bad = true
				}
				delete(pending, m.ToolCallID)
			}
		}
		if len(pending) > 0 {
			bad = true
		}
		if bad {
			*violations = append(*violations, 1)
			mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"broken tool history"}}`)
			return
		}
		// Validate that the next response only calls tools offered here.
		if strictCalls && idx < len(replies) {
			var shape struct {
				Choices []struct {
					Message struct {
						ToolCalls []struct {
							Function struct {
								Name string `json:"name"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"message"`
				} `json:"choices"`
			}
			_ = json.Unmarshal([]byte(replies[idx]), &shape)
			if len(shape.Choices) > 0 {
				for _, tc := range shape.Choices[0].Message.ToolCalls {
					if !allowed[tc.Function.Name] {
						*violations = append(*violations, 1)
						mu.Unlock()
						w.WriteHeader(http.StatusBadRequest)
						_, _ = fmt.Fprintf(w, `{"error":{"message":"tool %s was not offered in this request"}}`, tc.Function.Name)
						return
					}
				}
			}
		}
		idx++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[idx-1])
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "m")
	c.PinContext(16384)
	c.SetRetry(0, 0)
	return c, toolsSeen, violations
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func discoverScript(declared string) []string {
	plan := `{"summary":"s","steps":["a"],"commands":["go test ./..."]` + declared + `}`
	return []string{
		fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[%s]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, toolCallJSON("1", "mcp_find_tool", `{"query":"danger"}`)),
		fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[%s]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, toolCallJSON("2", "propose_plan", plan)),
		fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[%s]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, toolCallJSON("3", "big__danger", `{}`)),
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"4","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
}

func runDiscoverAgent(t *testing.T, trust, declared string, confirm agentConfirmerLike) (string, []string, []int) {
	t.Helper()
	reg, _ := discoverRegistry(t, trust)
	client, seen, violations := strictDiscoverProvider(t, discoverScript(declared), true)
	var out strings.Builder
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: confirm, Out: &out, Err: io.Discard,
		RequirePlan: true, MaxSteps: 8, Intent: "build",
	}, "sys", "change a file"); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String(), *seen, *violations
}

type agentConfirmerLike interface {
	Confirm(string, string) (ui.Decision, string, error)
	ConfirmForced(string, string) (ui.Decision, string, error)
}

func TestMCPFoundToolIsCallableInTheSameTurn(t *testing.T) {
	conf := &countingConfirm{d: ui.Approve}
	out, seen, violations := runDiscoverAgent(t, "ask", `,"tools":["big__danger"]`, conf)
	if len(violations) != 0 {
		t.Fatalf("strict provider violations %v", violations)
	}
	if !strings.Contains(out, "(in plan)") || strings.Contains(out, "outside plan") {
		t.Fatalf("declared discovered tool plan result:\n%s", out)
	}
	if conf.calls != 1 {
		t.Fatalf("confirmations %d", conf.calls)
	}
	// The call request after the find must carry the discovered schema.
	found := false
	for _, names := range seen {
		if strings.Contains(names, "big__danger") {
			found = true
		}
	}
	if !found {
		t.Fatalf("discovered schema never appeared in a follow-up request: %v", seen)
	}
}

func TestMCPFoundToolOutsidePlanAsksAgain(t *testing.T) {
	conf := &countingConfirm{d: ui.Approve}
	out, _, violations := runDiscoverAgent(t, "ask", "", conf)
	if len(violations) != 0 {
		t.Fatalf("strict provider violations %v", violations)
	}
	if !strings.Contains(out, "outside plan") {
		t.Fatalf("outside-plan found tool not marked:\n%s", out)
	}
	if conf.calls < 2 {
		t.Fatalf("outside plan did not ask again: %d", conf.calls)
	}
}

func TestMCPReadOnlyTrustHidesMutatingFromFind(t *testing.T) {
	profile := discoverProfile("read_only")
	caller := &discoverCaller{tools: map[string][]mcp.Tool{"big": {
		discoverCall("danger", false), discoverCall("read", true),
	}}}
	bridge := tools.NewMCPBridge(caller, profile)
	exposed, err := bridge.Exposed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range exposed {
		if e.MCPTool == "danger" {
			t.Fatalf("read_only trust exposed mutating tool: %+v", exposed)
		}
	}
	meta := bridge.FindToolFor(exposed)
	act, err := meta.Prepare(`{"query":"danger"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "big__danger") {
		t.Fatalf("find returned a hidden mutating tool:\n%s", res.Output)
	}
}

func TestMCPDiscoverDoesNotResurrectUnknownNames(t *testing.T) {
	// A find result only registers names that were actually returned; calling
	// anything else stays an unknown tool.
	script := []string{
		fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[%s]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, toolCallJSON("1", "mcp_find_tool", `{"query":"danger"}`)),
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"big__nope","arguments":"{}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	reg, _ := discoverRegistry(t, "ask")
	client, _, _ := strictDiscoverProvider(t, script, false)
	var out strings.Builder
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve}, Out: &out, Err: io.Discard,
		MaxSteps: 4, Intent: "act",
	}, "sys", "do it"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "unknown tool") {
		t.Fatalf("unknown discovered name was not refused:\n%s", out.String())
	}
}
