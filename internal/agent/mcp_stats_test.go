package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// fakeMCPTool is a stand-in for a bridge tool: it carries the MCP identity the
// agent counts and returns one read-only result.
type fakeMCPTool struct{}

func (fakeMCPTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{Type: "function", Function: llm.FunctionSpec{
		Name: "mcp_fake", Description: "fake mcp", Parameters: json.RawMessage(`{"type":"object"}`),
	}}
}
func (fakeMCPTool) Prepare(string) (tools.Action, error) { return fakeMCPAction{}, nil }

type fakeMCPAction struct{}

func (fakeMCPAction) Summary() string        { return "mcp fake" }
func (fakeMCPAction) Preview() string        { return "mcp fake" }
func (fakeMCPAction) Kind() tools.ActionKind { return tools.ReadOnly }
func (fakeMCPAction) MCPServer() string      { return "fake" }
func (fakeMCPAction) MCPTool() string        { return "tool" }
func (fakeMCPAction) Run(context.Context) (tools.Result, error) {
	return tools.Result{Output: "<untrusted_mcp_result>ok</untrusted_mcp_result>"}, nil
}

func TestAgentCountsMCPCalls(t *testing.T) {
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"mcp_fake","arguments":"{}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	hit := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
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
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(g, io.Discard, nil, nil, nil).Add(fakeMCPTool{})
	var stats Stats
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, Err: io.Discard, Stats: &stats, MaxSteps: 4, Intent: "act",
	}, "sys", "do it"); err != nil {
		t.Fatal(err)
	}
	if stats.MCPCalls != 1 {
		t.Fatalf("MCPCalls = %d, want 1", stats.MCPCalls)
	}
}
