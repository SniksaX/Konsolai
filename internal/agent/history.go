package agent

import (
	"context"

	"github.com/SniksaX/Konsolai/internal/llm"
)

// RecordMissingToolReplies makes the recorded history valid after an
// interrupted or early-ended step: every tool call in the trailing assistant
// message that has no reply gets a synthetic "not executed: turn ended" reply,
// in tool_call order. It runs after Run so a later resume never loads a
// dangling tool_calls group.
func RecordMissingToolReplies(ctx context.Context, rec Recorder, msgs []llm.Message) {
	if rec == nil {
		return
	}
	missing := llm.MissingToolReplies(msgs)
	if len(missing) == 0 {
		return
	}
	// A canceled turn must still record its synthetic replies.
	ctx = context.WithoutCancel(ctx)
	for _, reply := range missing {
		_ = rec.Message(ctx, reply)
	}
}
