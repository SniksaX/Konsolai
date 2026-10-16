package turn

import (
	"context"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
)

func TestHistoryWithSummary(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "old1"},
		{Role: llm.RoleAssistant, Content: "old2"},
		{Role: llm.RoleUser, Content: "new"},
	}
	got := HistoryWithSummary(msgs, "we decided X", 2)
	if len(got) != 1 {
		t.Fatalf("len %d %+v", len(got), got)
	}
	if !strings.Contains(got[0].Content, "Summary of earlier conversation: we decided X") || !strings.Contains(got[0].Content, "new") {
		t.Fatalf("%q", got[0].Content)
	}
	if got[0].Role != llm.RoleUser {
		t.Fatalf("%+v", got)
	}
}

func TestLoadCompactedLocalSummary(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "old goal"},
		{Role: llm.RoleAssistant, Content: "did it"},
		{Role: llm.RoleUser, Content: "next"},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "recent"},
	}
	if err := memory.SaveLast(memory.LastSession{
		Profile: "p", CWD: cwd, Title: "t", Messages: msgs,
		CompactSummary: "we decided X", CompactedUpto: 2,
	}); err != nil {
		t.Fatal(err)
	}
	got := LoadCompacted(context.Background(), memory.NewGuard(nil, nil), "", "p", cwd)
	if len(got) != 3 {
		t.Fatalf("len %d %+v", len(got), got)
	}
	if !strings.Contains(got[0].Content, "Summary of earlier conversation: we decided X") || !strings.Contains(got[0].Content, "next") {
		t.Fatalf("%q", got[0].Content)
	}
}

func TestCompactionContinueAlternatingRoles(t *testing.T) {
	hist := []llm.Message{
		{Role: llm.RoleUser, Content: "old1"},
		{Role: llm.RoleAssistant, Content: "old2"},
		{Role: llm.RoleUser, Content: "keep"},
		{Role: llm.RoleAssistant, Content: "ok"},
	}
	got := HistoryWithSummary(hist, "we decided X", 2)
	got = append(got, llm.Message{Role: llm.RoleUser, Content: "continue"})
	got = llm.Normalize(got)
	prev := llm.Role("")
	for _, m := range got {
		if m.Role == llm.RoleSystem {
			continue
		}
		if m.Role == prev {
			t.Fatalf("consecutive %s", m.Role)
		}
		prev = m.Role
	}
	if got[0].Role != llm.RoleUser {
		t.Fatal("first must be user")
	}
}

func TestShouldCompactThreshold(t *testing.T) {
	if ShouldCompact(0, 1000) || ShouldCompact(700, 1000) {
		t.Fatal("700/1000 is not above 70%")
	}
	if !ShouldCompact(701, 1000) {
		t.Fatal("701/1000 should compact")
	}
	if ShouldCompact(100, 0) {
		t.Fatal("zero window")
	}
}
