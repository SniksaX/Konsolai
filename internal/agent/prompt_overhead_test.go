package agent

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
)

// The measurement inputs below are fixed samples, so the numbers in the logs
// are reproducible across machines; the live snapshot varies by host but has
// roughly the same shape.
const overheadSnapshot = `system: linux/amd64
shell: bash
cwd: /srv/project
package manager: pacman
git branch: dev, 3 changed, ahead 2
terminal: 120x32
user: developer`

const overheadFacts = `

<memory_background>
- project uses Go 1.27 and the standard test cache
- the user prefers table-driven tests and short commits
- the last turn touched internal/agent/elision.go
</memory_background>`

const overheadTask = "fix the failing read dedupe test"

const overheadRatio = 0.30

func overheadTokens(msgs []llm.Message, specs []llm.ToolSpec) int {
	chars := llm.MessageChars(msgs) + llm.ToolsSchemaChars(specs)
	return int(float64(chars)*overheadRatio + 0.5)
}

// overheadTools builds the tool sets each intent would offer.
func overheadTools(t *testing.T) (lookup, all []llm.ToolSpec) {
	t.Helper()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(g, io.Discard, nil, nil, nil)
	lookup = reg.Named("list_dir", "read_file", "outline", "search", "find_files", "web_fetch", "run_shell", "finish").Specs()
	all = reg.Specs()
	return lookup, all
}

type overheadMCPCaller struct {
	tools []mcp.Tool
}

func (c overheadMCPCaller) Tools(context.Context, string) ([]mcp.Tool, error) {
	return c.tools, nil
}

func (c overheadMCPCaller) Call(context.Context, string, string, map[string]any) (mcp.Result, error) {
	return mcp.Result{}, nil
}

// overheadMCP builds the model-visible MCP schemas for zero or one fake
// server with six tools.
func overheadMCP(t *testing.T, count int) []llm.ToolSpec {
	t.Helper()
	if count == 0 {
		return nil
	}
	toolList := make([]mcp.Tool, 0, count)
	for i := 0; i < count; i++ {
		toolList = append(toolList, mcp.Tool{
			Name:        "tool_" + string(rune('a'+i)),
			Description: "A fake MCP tool used to measure schema weight.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search text"},"limit":{"type":"integer"}}}`),
			ReadOnly:    true,
		})
	}
	caller := overheadMCPCaller{tools: toolList}
	cfg := config.Builtin()
	cfg.MCP.Servers = map[string]config.MCPServer{
		"fake": {Transport: "stdio", Command: "fake-server", Enabled: true, Trust: config.MCPTrustAsk},
	}
	bridge := tools.NewMCPBridge(caller, cfg)
	extra, err := bridge.Tools(context.Background())
	if err != nil {
		t.Fatalf("bridge tools: %v", err)
	}
	specs := make([]llm.ToolSpec, len(extra))
	for i, tool := range extra {
		specs[i] = tool.Spec()
	}
	return specs
}

type overheadRow struct {
	intent      string
	prompt      int
	snapshot    int
	facts       int
	tools       int
	mcp         int
	total       int
	toolCount   int
	mcpToolName string
}

func overheadMeasure(t *testing.T, intent, prompt string, specs, mcpSpecs []llm.ToolSpec) overheadRow {
	t.Helper()
	system := prompt + "\n\n" + overheadSnapshot + overheadFacts
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: overheadTask},
	}
	allSpecs := append(append([]llm.ToolSpec{}, specs...), mcpSpecs...)
	promptTokens := overheadTokens([]llm.Message{{Role: llm.RoleSystem, Content: prompt}}, nil)
	snapshotTokens := overheadTokens([]llm.Message{{Role: llm.RoleSystem, Content: overheadSnapshot}}, nil)
	factsTokens := overheadTokens([]llm.Message{{Role: llm.RoleSystem, Content: overheadFacts}}, nil)
	toolTokens := overheadTokens(nil, specs)
	mcpTokens := overheadTokens(nil, mcpSpecs)
	return overheadRow{
		intent: intent, prompt: promptTokens, snapshot: snapshotTokens, facts: factsTokens,
		tools: toolTokens, mcp: mcpTokens,
		total:     overheadTokens(msgs, allSpecs),
		toolCount: len(specs) + len(mcpSpecs),
	}
}

// TestPromptOverheadReport measures the per-call prompt weight of each intent:
// the system prompt, the environment snapshot, the memory facts, the tool
// schemas, and the MCP schemas. It pins the ordering so a regression that
// quietly adds a payload fails here; the logged numbers are the report.
func TestPromptOverheadReport(t *testing.T) {
	lookupSpecs, allSpecs := overheadTools(t)
	mcpSix := overheadMCP(t, 6)

	answer := overheadMeasure(t, "answer", AnswerPrompt, nil, nil)
	lookup := overheadMeasure(t, "lookup", LookupPrompt, lookupSpecs, nil)
	act := overheadMeasure(t, "act", ActPrompt+"\n"+ScopeRule, allSpecs, nil)
	build := overheadMeasure(t, "build", CodePrompt+"\n"+ScopeRule, allSpecs, nil)
	actMCP := overheadMeasure(t, "act + mcp", ActPrompt+"\n"+ScopeRule, allSpecs, mcpSix)

	rows := []overheadRow{answer, lookup, act, build, actMCP}
	for _, row := range rows {
		t.Logf("%-10s total=%5d  prompt=%4d snapshot=%4d facts=%4d tools=%4d (%2d) mcp=%4d (%2d)",
			row.intent, row.total, row.prompt, row.snapshot, row.facts, row.tools, row.toolCount, row.mcp, len(mcpSix))
	}
	if !(answer.total < lookup.total && lookup.total < act.total) {
		t.Fatalf("intent weight order is wrong: answer=%d lookup=%d act=%d", answer.total, lookup.total, act.total)
	}
	if build.total <= lookup.total {
		t.Fatalf("build payload is lighter than lookup: build=%d lookup=%d", build.total, lookup.total)
	}
	if answer.tools != 0 || answer.mcp != 0 {
		t.Fatalf("the answer intent carries tool schemas: %+v", answer)
	}
	if mcpSix == nil || len(mcpSix) != 6 {
		t.Fatalf("expected six MCP schemas, got %d", len(mcpSix))
	}
	if actMCP.mcp <= 0 || actMCP.total <= act.total {
		t.Fatalf("MCP schemas add no weight: act=%+v act+mcp=%+v", act, actMCP)
	}
	if overheadMCP(t, 0) != nil {
		t.Fatal("MCP schemas exist with no configured server")
	}
}

// TestPromptOverheadExistingCuts measures the intent-specific narrowing that is
// already in place: lookup drops the mutating tools and a plan-gated first step
// drops everything but exploration and propose_plan.
func TestPromptOverheadExistingCuts(t *testing.T) {
	lookupSpecs, allSpecs := overheadTools(t)
	all := overheadTokens(nil, allSpecs)
	lookupOnly := overheadTokens(nil, lookupSpecs)
	prePlan := overheadTokens(nil, prePlanSpecs(allSpecs))
	t.Logf("tool schemas: all=%d lookup=%d (saves %d, %.0f%%) pre-plan=%d (saves %d, %.0f%%)",
		all, lookupOnly, all-lookupOnly, 100*float64(all-lookupOnly)/float64(all),
		prePlan, all-prePlan, 100*float64(all-prePlan)/float64(all))
	if lookupOnly >= all {
		t.Fatalf("lookup narrowing does not save tokens: all=%d lookup=%d", all, lookupOnly)
	}
	if prePlan >= all {
		t.Fatalf("pre-plan narrowing does not save tokens: all=%d pre-plan=%d", all, prePlan)
	}
	if len(prePlanSpecs(allSpecs)) >= len(allSpecs) {
		t.Fatalf("pre-plan keeps every tool: %d", len(prePlanSpecs(allSpecs)))
	}
}
