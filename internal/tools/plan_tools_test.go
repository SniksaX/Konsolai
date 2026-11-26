package tools

import (
	"strings"
	"testing"
)

func TestProposePlanDeclaresMCPTools(t *testing.T) {
	tool := newProposePlan(false, "do it")
	act, err := tool.Prepare(`{"summary":"s","steps":["a"],"commands":["go test ./..."],"tools":["github__create_issue","fs:read_file"]}`)
	if err != nil {
		t.Fatal(err)
	}
	declared, ok := act.(interface{ PlanTools() []string })
	if !ok {
		t.Fatal("plan action does not expose PlanTools")
	}
	got := declared.PlanTools()
	if len(got) != 2 || got[0] != "github__create_issue" || got[1] != "fs:read_file" {
		t.Fatalf("declared %v", got)
	}
	preview := act.Preview()
	for _, want := range []string{"mcp tools:", "github: create_issue", "fs: read_file"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview misses %q:\n%s", want, preview)
		}
	}
	if !strings.Contains(act.(interface{ PlanText() string }).PlanText(), "github__create_issue") {
		t.Fatal("plan text misses the declared tool")
	}
	if _, err := tool.Prepare(`{"summary":"s","steps":["a"],"commands":["go test ./..."],"tools":["bad name"]}`); err == nil {
		t.Fatal("a tool name with spaces must be rejected")
	}
}
