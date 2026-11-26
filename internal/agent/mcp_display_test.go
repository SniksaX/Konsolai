package agent

import (
	"strings"
	"testing"
)

func TestFormatStatsLineCountsMCPCalls(t *testing.T) {
	s := Stats{Steps: 1, MaxStep: 0}
	if line := FormatStatsLine(s); strings.Contains(line, "mcp=") {
		t.Fatalf("a turn with no MCP call printed one: %q", line)
	}
	s.MCPCalls = 2
	line := FormatStatsLine(s)
	if !strings.Contains(line, "mcp=2") {
		t.Fatalf("stats line misses the MCP call count: %q", line)
	}
}

func TestFormatSummaryKeepsTheMCPCallsOutOfTheSummary(t *testing.T) {
	s := Stats{Steps: 1, CompletionTokens: 10, MCPCalls: 3}
	if got := FormatSummary("answer", s); strings.Contains(got, "mcp") {
		t.Fatalf("the end-of-turn summary counts MCP calls: %q", got)
	}
}
