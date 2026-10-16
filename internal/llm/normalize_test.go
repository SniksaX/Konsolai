package llm

import (
	"strings"
	"testing"
)

func TestNormalizeMergesUsersAndDropsEmptyAssistant(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "Summary of earlier conversation: X"},
		{Role: RoleUser, Content: "continue"},
		{Role: RoleAssistant, Content: "   "},
		{Role: RoleAssistant, Content: "ok"},
	}
	got := Normalize(msgs)
	if len(got) != 3 {
		t.Fatalf("len %d %+v", len(got), got)
	}
	if got[0].Role != RoleSystem || got[1].Role != RoleUser || got[2].Role != RoleAssistant {
		t.Fatalf("roles %+v", got)
	}
	if !strings.Contains(got[1].Content, "Summary of earlier conversation: X") || !strings.Contains(got[1].Content, "continue") {
		t.Fatalf("%q", got[1].Content)
	}
	if !strings.Contains(got[1].Content, "\n\n") {
		t.Fatal("users should be joined with a blank line")
	}
}

func TestNormalizeAlternatingAfterCompactionContinue(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "Summary of earlier conversation: decided X"},
		{Role: RoleUser, Content: "continue"},
	}
	got := Normalize(msgs)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	prev := RoleSystem
	for _, m := range got {
		if m.Role != RoleSystem && m.Role == prev {
			t.Fatalf("not alternating %s then %s", prev, m.Role)
		}
		if m.Role != RoleSystem {
			prev = m.Role
		}
	}
	if got[1].Role != RoleUser {
		t.Fatal("first non-system must be user")
	}
}

func TestNormalizeDropsLeadingAssistantAndOrphanTools(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleAssistant, Content: "stale", ToolCalls: []ToolCall{{ID: "1"}}},
		{Role: RoleTool, Content: "out", ToolCallID: "1"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleTool, Content: "orphan", ToolCallID: "x"},
		{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{{ID: "2", Function: FunctionCall{Name: "finish"}}}},
		{Role: RoleTool, Content: "done", ToolCallID: "2"},
	}
	got := Normalize(msgs)
	roles := rolesOf(got)
	if roles != "system user assistant tool" {
		t.Fatalf("roles %s", roles)
	}
}

func rolesOf(msgs []Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(string(m.Role))
	}
	return b.String()
}
