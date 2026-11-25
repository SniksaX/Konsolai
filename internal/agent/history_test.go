package agent

import (
	"context"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/tools"
)

type captureRecorder struct {
	msgs []llm.Message
}

func (r *captureRecorder) Message(_ context.Context, m llm.Message) error {
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *captureRecorder) Action(context.Context, ActionRecord) error { return nil }

func (r *captureRecorder) FileChange(context.Context, tools.FileChange) error { return nil }

func testToolCall(id string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: "read_file"}}
}

func TestRecordMissingToolRepliesRepairsATrailingGroup(t *testing.T) {
	rec := &captureRecorder{}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{testToolCall("c2"), testToolCall("c1")}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "one"},
	}
	RecordMissingToolReplies(context.Background(), rec, msgs)
	if len(rec.msgs) != 1 {
		t.Fatalf("recorded %d replies", len(rec.msgs))
	}
	got := rec.msgs[0]
	if got.Role != llm.RoleTool || got.ToolCallID != "c2" || got.Content != llm.MissingToolReply {
		t.Fatalf("synthetic reply %+v", got)
	}
}

func TestRecordMissingToolRepliesLeavesACompleteGroupAlone(t *testing.T) {
	rec := &captureRecorder{}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{testToolCall("c1")}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "one"},
	}
	RecordMissingToolReplies(context.Background(), rec, msgs)
	if len(rec.msgs) != 0 {
		t.Fatalf("recorded %d replies for a complete group", len(rec.msgs))
	}
}
