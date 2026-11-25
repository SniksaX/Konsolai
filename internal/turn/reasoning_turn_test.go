package turn

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
)

// reasoningToolTurn is the first streamed reply: thinking text, then one
// run_shell tool call, then a normal final answer on the retry.
func reasoningToolTurn(t *testing.T) func(int, []llm.Message) (string, bool) {
	t.Helper()
	return func(n int, _ []llm.Message) (string, bool) {
		if n != 1 {
			return jsonAnswer("done"), false
		}
		var b strings.Builder
		chunk := func(payload map[string]any) {
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString("data: " + string(raw) + "\n\n")
		}
		chunk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"reasoning_content": "SECRET-THOUGHT-1 "}}}})
		chunk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"reasoning_content": "SECRET-THOUGHT-2"}}}})
		chunk(map[string]any{"choices": []map[string]any{{
			"delta": map[string]any{"tool_calls": []map[string]any{{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "run_shell", "arguments": `{"cmd":"echo hi"}`},
			}}},
			"finish_reason": "tool_calls",
		}}})
		b.WriteString("data: [DONE]\n\n")
		return b.String(), true
	}
}

func TestReasoningStaysInsideTheToolTurn(t *testing.T) {
	var mu sync.Mutex
	var replay llm.Message
	var replayHasTools bool
	fd := newFakeDeepSeek(t, reasoningToolTurn(t))
	fd.inspect = func(msgs []llm.Message, hasTools bool) {
		for _, m := range msgs {
			if m.Role == llm.RoleAssistant && m.ReasoningContent != "" {
				mu.Lock()
				if hasTools {
					replay = m
					replayHasTools = true
				}
				mu.Unlock()
				return
			}
		}
	}
	mem := memory.NewGuard(nil, func(error) {})
	r := historyRunner(t, fd, mem, autoConfirm{})
	cwd := t.TempDir()
	res := r.RunTurn(context.Background(), Request{Text: "run the echo", Intent: "act", Yes: true, Cwd: cwd, New: true})
	if len(fd.violations) > 0 {
		t.Fatalf("the turn sent an invalid list:\n%s", fd.evidence())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, r.Err)
	}
	mu.Lock()
	got := replay
	hasTools := replayHasTools
	mu.Unlock()
	if !hasTools || !strings.Contains(got.ReasoningContent, "SECRET-THOUGHT") {
		t.Fatalf("the tool request did not replay reasoning_content: %+v", got)
	}
	if strings.Contains(res.Answer, "SECRET-THOUGHT") {
		t.Fatalf("reasoning leaked into the answer: %q", res.Answer)
	}
	last, err := os.ReadFile(memory.LastPath())
	if err != nil {
		t.Fatalf("last session: %v", err)
	}
	if strings.Contains(string(last), "SECRET-THOUGHT") {
		t.Fatalf("reasoning leaked into the saved session:\n%s", last)
	}
}
