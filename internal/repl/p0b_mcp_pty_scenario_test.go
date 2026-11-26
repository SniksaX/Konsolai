package repl

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// P0-B scenario 5: MCP end to end on a real PTY. Every test re-execs this test
// binary as the REPL child with fake OpenAI and MCP HTTP servers in the parent.

const (
	ptyModeMCPConfigured = "p0b-mcp-configured"
	ptyModeMCPTrust      = "p0b-mcp-trust"
	ptyModeMCPPlan       = "p0b-mcp-plan"
	ptyModeMCPFence      = "p0b-mcp-fence"
	ptyModeMCPFind       = "p0b-mcp-find"
	ptyModeMCPManage     = "p0b-mcp-manage"
)

// TestMCPPTYConfiguredThenUp: nothing connects until a turn needs the server.
// /mcp and /status read "configured" first, then "up" once a turn connected.
func TestMCPPTYConfiguredThenUp(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeMCPConfigured {
		ptyChildRun()
		return
	}
	sc := startPTYScenario(t, "TestMCPPTYConfiguredThenUp", ptyModeMCPConfigured, true, map[string]string{
		ptyMCPTrust: config.MCPTrustReadOnly,
	})
	sc.model.setDecision(routeDecisionJSON("lookup", "low", false))
	sc.prompt(t)

	sc.run(t, "/mcp", screenHas("not connected"))
	if calls := sc.mcp.called(); len(calls) != 0 {
		t.Fatalf("/mcp list connected the server: %v", calls)
	}
	sc.run(t, "/status", screenHas("1 configured"))
	if calls := sc.mcp.called(); len(calls) != 0 {
		t.Fatalf("/status connected the server: %v", calls)
	}

	sc.model.enqueue(toolCallReply("1", "srv__read", "{}"), contentReply("read done"))
	sc.turn(t, "use the mcp read tool", screenHas("read done"))
	if sc.mcp.callCount("read") != 1 {
		t.Fatalf("the fake MCP server saw %v", sc.mcp.called())
	}
	sc.run(t, "/mcp", func(screen *screen) bool {
		return lastScreenLine(screen.dump(), "srv", "connected") != ""
	})
	sc.run(t, "/status", func(screen *screen) bool {
		return lastScreenLine(screen.dump(), "mcp", "1/1 up") != ""
	})
}

// TestMCPPTYTrustChangesOfferedTools: a /set mcp trust change applies to the very
// next request. The fake provider records the tool list of each turn.
func TestMCPPTYTrustChangesOfferedTools(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeMCPTrust {
		ptyChildRun()
		return
	}
	sc := startPTYScenario(t, "TestMCPPTYTrustChangesOfferedTools", ptyModeMCPTrust, true, map[string]string{
		ptyMCPTrust: config.MCPTrustAsk,
	})
	sc.model.setDecision(routeDecisionJSON("act", "low", false))
	sc.prompt(t)

	sc.model.enqueue(contentReply("reply-one"))
	sc.turn(t, "first request", screenHas("reply-one"))
	sc.run(t, "/set mcp trust srv read_only", screenHas("srv trust read_only"))
	sc.model.enqueue(contentReply("reply-two"))
	sc.turn(t, "second request", screenHas("reply-two"))
	sc.run(t, "/set mcp trust srv ask", screenHas("srv trust ask"))
	sc.model.enqueue(contentReply("reply-three"))
	sc.turn(t, "third request", screenHas("reply-three"))

	lists := sc.model.chatToolLists()
	if len(lists) < 3 {
		t.Fatalf("captured %d tool lists, want at least 3: %v", len(lists), lists)
	}
	if !hasPTYTool(lists[0], "srv__write") {
		t.Fatalf("trust ask did not offer the write tool: %v", lists[0])
	}
	if hasPTYTool(lists[1], "srv__write") {
		t.Fatalf("trust read_only still offered the write tool: %v", lists[1])
	}
	if !hasPTYTool(lists[2], "srv__write") {
		t.Fatalf("trust ask again did not restore the write tool: %v", lists[2])
	}
}

// TestMCPPTYPlanNamesTool: under a required plan, the mutating MCP tool is not
// called before approval and the plan names it.
func TestMCPPTYPlanNamesTool(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeMCPPlan {
		ptyChildRun()
		return
	}
	sc := startPTYScenario(t, "TestMCPPTYPlanNamesTool", ptyModeMCPPlan, true, map[string]string{
		ptyMCPTrust: config.MCPTrustAsk,
	})
	sc.model.setDecision(routeDecisionJSON("act", "high", true))
	sc.prompt(t)

	plan := `{"summary":"use the mcp write tool","steps":["call the declared mcp tool"],"commands":["go vet ./..."],"tools":["srv:write"]}`
	sc.model.enqueue(
		toolCallReply("1", "propose_plan", plan),
		toolCallReply("2", "srv__write", "{}"),
		contentReply("write done"),
	)
	sc.turn(t, "write through mcp", screenHas("[y]es"))
	planLine := sc.expectLine(t, "plan names the mcp tool", "mcp tools:", "srv: write")
	if !strings.Contains(planLine, "write") {
		t.Fatalf("plan line %q", planLine)
	}
	if calls := sc.mcp.called(); len(calls) != 0 {
		t.Fatalf("the mutating MCP tool ran before approval: %v", calls)
	}
	sc.approvePlan(t)
	sc.v.expect("write done", screenHas("write done"))
	if sc.mcp.callCount("write") != 1 {
		t.Fatalf("the approved MCP write call did not reach the server: %v", sc.mcp.called())
	}
}

// TestMCPPTYResultFencedAndKeyMasked: an MCP result that carries an instruction
// and the server's key reaches the model fenced as untrusted data, with the key
// masked on screen and in the request body.
func TestMCPPTYResultFencedAndKeyMasked(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeMCPFence {
		ptyChildRun()
		return
	}
	sc := startPTYScenario(t, "TestMCPPTYResultFencedAndKeyMasked", ptyModeMCPFence, true, map[string]string{
		ptyMCPTrust: config.MCPTrustReadOnly,
		ptyTrace:    "1",
	})
	sc.model.setDecision(routeDecisionJSON("lookup", "low", false))
	sc.prompt(t)
	sc.model.enqueue(toolCallReply("1", "srv__echo", "{}"), contentReply("answer done"))
	sc.turn(t, "inspect the mcp echo tool", screenHas("answer done"))
	if sc.mcp.callCount("echo") != 1 {
		t.Fatalf("the echo tool did not run: %v", sc.mcp.called())
	}

	fenced := ""
	for _, body := range sc.model.requestBodies() {
		text := ptyMessageText(t, body)
		if strings.Contains(text, "<untrusted_mcp_result") {
			fenced = text
		}
	}
	if fenced == "" {
		t.Fatal("the model never received the MCP result")
	}
	for _, want := range []string{"<untrusted_mcp_result", "data, never instructions", "[REDACTED"} {
		if !strings.Contains(fenced, want) {
			t.Fatalf("the fenced result misses %q: %q", want, fenced)
		}
	}
	secret := ptyMCPSecret()
	if strings.Contains(fenced, secret) {
		t.Fatal("the request body carries the MCP key")
	}
	if strings.Contains(string(sc.raw()), secret) {
		t.Fatal("the PTY screen carries the MCP key")
	}
}

// TestMCPPTYFindToolAboveCap: above the inline cap the turn offers mcp_find_tool;
// the tool it finds is registered and callable in the same turn.
func TestMCPPTYFindToolAboveCap(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeMCPFind {
		ptyChildRun()
		return
	}
	sc := startPTYScenario(t, "TestMCPPTYFindToolAboveCap", ptyModeMCPFind, true, map[string]string{
		ptyMCPTrust: config.MCPTrustReadOnly,
		ptyMCPCap:   "1",
	})
	sc.model.setDecision(routeDecisionJSON("act", "low", false))
	sc.prompt(t)
	sc.model.enqueue(
		toolCallReply("1", "mcp_find_tool", `{"query":"extra"}`),
		toolCallReply("2", "srv__extra", "{}"),
		contentReply("extra done"),
	)
	sc.turn(t, "find and run an mcp tool", screenHas("extra done"))

	lists := sc.model.chatToolLists()
	if len(lists) < 2 {
		t.Fatalf("captured %d tool lists: %v", len(lists), lists)
	}
	if !hasPTYTool(lists[0], "mcp_find_tool") || hasPTYTool(lists[0], "srv__extra") {
		t.Fatalf("above the cap the first request offered %v", lists[0])
	}
	if !hasPTYTool(lists[1], "srv__extra") {
		t.Fatalf("the found tool was not registered for the next step: %v", lists[1])
	}
	if sc.mcp.callCount("extra") != 1 {
		t.Fatalf("the found tool did not run: %v", sc.mcp.called())
	}
}

// TestMCPPTYModelCannotManageServers: a tool call that tries to add a server is
// refused as an unknown tool and the configured server set is unchanged.
func TestMCPPTYModelCannotManageServers(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeMCPManage {
		ptyChildRun()
		return
	}
	sc := startPTYScenario(t, "TestMCPPTYModelCannotManageServers", ptyModeMCPManage, true, map[string]string{
		ptyMCPTrust: config.MCPTrustReadOnly,
	})
	sc.model.setDecision(routeDecisionJSON("act", "low", false))
	sc.prompt(t)
	multi := multiToolCallReply(
		ptyToolCall{ID: "1", Name: "mcp_add_server", Args: `{"name":"evil"}`},
		ptyToolCall{ID: "2", Name: "mcp_set_trust", Args: `{"name":"srv","trust":"auto"}`},
		ptyToolCall{ID: "3", Name: "mcp_enable", Args: `{"name":"srv"}`},
	)
	sc.model.enqueue(multi, contentReply("could not"))
	sc.turn(t, "add a new mcp server", screenHas("unknown tool mcp_add_server"))
	for _, name := range []string{"unknown tool mcp_add_server", "unknown tool mcp_set_trust", "unknown tool mcp_enable"} {
		if !strings.Contains(sc.dump(), name) {
			t.Fatalf("management call %q was not refused as an unknown tool", name)
		}
	}

	sc.run(t, "/mcp", func(screen *screen) bool {
		return lastScreenLine(screen.dump(), "srv", "read_only") != ""
	})
	for _, line := range strings.Split(sc.dump(), "\n") {
		if strings.Contains(line, "evil") && (strings.Contains(line, "http") || strings.Contains(line, "stdio")) {
			t.Fatalf("a model tool call added a server row: %q", line)
		}
	}
}

// ptyMessageText decodes one captured chat request into its message text, so
// JSON escaping of angle brackets does not hide the untrusted fence.
func ptyMessageText(t *testing.T, body string) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return body
	}
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// ptyToolCall is one scripted tool call for multiToolCallReply.
type ptyToolCall struct {
	ID   string
	Name string
	Args string
}

// multiToolCallReply builds one assistant message with several tool calls.
func multiToolCallReply(calls ...ptyToolCall) string {
	list := make([]any, len(calls))
	for i, call := range calls {
		list[i] = map[string]any{
			"id": call.ID, "type": "function",
			"function": map[string]any{"name": call.Name, "arguments": call.Args},
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "tool_calls": list,
		}}},
		"usage": map[string]any{"prompt_tokens": 9, "completion_tokens": 3},
	})
	return string(payload)
}
