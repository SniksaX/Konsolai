package commands

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// trustAutoChoice walks the /set mcp menu and returns the auto trust entry.
func trustAutoChoice(t *testing.T) Choice {
	t.Helper()
	for _, level := range mcpSetLevel(nil) {
		if level.Value != "trust" {
			continue
		}
		for _, choice := range level.Next {
			if choice.Value == config.MCPTrustAuto {
				return choice
			}
		}
	}
	t.Fatal("the /set mcp menu has no auto trust entry")
	return Choice{}
}

// TestMCPTrustAutoMenuMatchesTheRule: auto trusts only the server's readOnlyHint
// and a mutating MCP call still goes through the plan and approval gate, so the
// menu must not promise that the server's tools run without asking.
func TestMCPTrustAutoMenuMatchesTheRule(t *testing.T) {
	auto := trustAutoChoice(t)
	desc := strings.ToLower(auto.Description)
	if strings.Contains(desc, "without asking") {
		t.Fatalf("the auto trust description still promises auto-approval: %q", auto.Description)
	}
	if !strings.Contains(desc, "read-only") {
		t.Fatalf("the auto trust description does not name the read-only hint: %q", auto.Description)
	}
	if !strings.Contains(desc, "mutating") || !strings.Contains(desc, "ask") {
		t.Fatalf("the auto trust description does not say mutating calls still ask: %q", auto.Description)
	}
	// The other two levels keep their meaning.
	for _, level := range mcpSetLevel(nil) {
		if level.Value != "trust" {
			continue
		}
		for _, choice := range level.Next {
			switch choice.Value {
			case config.MCPTrustAsk:
				if !strings.Contains(strings.ToLower(choice.Description), "ask") {
					t.Fatalf("the ask description %q", choice.Description)
				}
			case config.MCPTrustReadOnly:
				if !strings.Contains(strings.ToLower(choice.Description), "read-only") {
					t.Fatalf("the read_only description %q", choice.Description)
				}
			}
		}
	}
}
