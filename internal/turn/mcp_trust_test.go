package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/tools"
)

// trustCaller is a fake MCP manager: one server with a read-only tool and a
// mutating tool.
type trustCaller struct {
	tools map[string][]mcp.Tool
}

func (c *trustCaller) Tools(context.Context, string) ([]mcp.Tool, error) {
	return c.tools["srv"], nil
}

func (c *trustCaller) Call(context.Context, string, string, map[string]any) (mcp.Result, error) {
	return mcp.Result{}, nil
}

func trustTool(name string, readOnly bool) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: name,
		ReadOnly:    readOnly,
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

// toolListRecorder is a fake OpenAI provider that records the tool names
// offered in every chat request and answers each turn with finish.
type toolListRecorder struct {
	mu    sync.Mutex
	lists [][]string
}

func (r *toolListRecorder) handler(w http.ResponseWriter, req *http.Request) {
	if strings.HasSuffix(req.URL.Path, "/models") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m","max_model_len":8192}]}`)
		return
	}
	body, _ := io.ReadAll(req.Body)
	var payload struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(body, &payload)
	names := make([]string, 0, len(payload.Tools))
	for _, t := range payload.Tools {
		names = append(names, t.Function.Name)
	}
	r.mu.Lock()
	r.lists = append(r.lists, names)
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`)
}

func (r *toolListRecorder) turnToolLists() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.lists))
	for i, names := range r.lists {
		out[i] = append([]string(nil), names...)
	}
	return out
}

func mcpTrustProfile(trust string) config.Config {
	c := config.Builtin()
	c.Roles.Fast = ""
	c.Roles.Embed = ""
	c.Roles.Rerank = ""
	c.RAG.Enabled = false
	c.Database.URL = ""
	server := config.DefaultMCPServer()
	server.Transport = config.MCPTransportStdio
	server.Command = "fake-mcp"
	server.Trust = trust
	c.MCP.Servers = map[string]config.MCPServer{"srv": server}
	return c
}

func hasToolName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestMCPTrustChangeMidSessionReevaluatesOfferedTools is the security repro:
// the runtime is built with trust ask, the user then sets trust read_only and
// back. Each turn must offer the tools the current profile allows, not a stale
// snapshot from process start.
func TestMCPTrustChangeMidSessionReevaluatesOfferedTools(t *testing.T) {
	rec := &toolListRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	t.Cleanup(srv.Close)
	client := llm.New(srv.URL+"/v1", "m")
	client.PinContext(8192)
	client.SetRetry(0, 0)

	ask := mcpTrustProfile(config.MCPTrustAsk)
	readOnly := mcpTrustProfile(config.MCPTrustReadOnly)
	caller := &trustCaller{tools: map[string][]mcp.Tool{"srv": {
		trustTool("read_tool", true),
		trustTool("mutate_tool", false),
	}}}
	rt := &MCPRuntime{Profile: ask, Bridge: tools.NewMCPBridge(caller, ask), MaxInline: 15}

	r := testRunner(t, client, autoConfirm{})
	r.MCPRuntime = rt
	run := func(profile config.Config) {
		t.Helper()
		r.Profile = profile
		res := r.RunTurn(context.Background(), Request{
			Text: "use the mcp tools", Intent: "act", Yes: true,
			Cwd: t.TempDir(), New: true,
		})
		if res.ExitCode != 0 {
			t.Fatalf("trust %s: exit %d", profile.MCP.Servers["srv"].Trust, res.ExitCode)
		}
	}

	run(ask)
	run(readOnly)
	run(ask)

	lists := rec.turnToolLists()
	if len(lists) != 3 {
		t.Fatalf("recorded %d requests, want 3: %v", len(lists), lists)
	}
	readName, mutateName := "srv__read_tool", "srv__mutate_tool"
	if !hasToolName(lists[0], readName) || !hasToolName(lists[0], mutateName) {
		t.Fatalf("trust ask did not offer both tools: %v", lists[0])
	}
	if !hasToolName(lists[1], readName) {
		t.Fatalf("trust read_only dropped the read-only tool: %v", lists[1])
	}
	if hasToolName(lists[1], mutateName) {
		t.Fatalf("trust read_only still offered the mutating tool (stale catalog): %v", lists[1])
	}
	if !hasToolName(lists[2], readName) || !hasToolName(lists[2], mutateName) {
		t.Fatalf("trust ask again did not restore both tools: %v", lists[2])
	}
}
