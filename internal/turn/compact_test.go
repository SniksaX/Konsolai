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
	if len(got) != 2 {
		t.Fatalf("len %d", len(got))
	}
	if !strings.Contains(got[0].Content, "Summary of earlier conversation: we decided X") {
		t.Fatalf("%q", got[0].Content)
	}
	if got[0].Role != llm.RoleUser || got[1].Content != "new" {
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
	if len(got) != 4 {
		t.Fatalf("len %d %+v", len(got), got)
	}
	if !strings.Contains(got[0].Content, "Summary of earlier conversation: we decided X") {
		t.Fatalf("%q", got[0].Content)
	}
	if got[1].Content != "next" {
		t.Fatalf("%q", got[1].Content)
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
