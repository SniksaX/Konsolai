package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

// TestCopyTakesThePlainAnswer proves the copy path never receives the panel
// frame: the stored answer is plain markdown and comes back byte for byte.
func TestCopyTakesThePlainAnswer(t *testing.T) {
	answer := "## Summary\n\nEdited `internal/ui/panel.go`.\n\nRun it yourself:\n\n```sh\ngo test ./...\n```\n"
	env, _, errb := testEnv(t)
	var got string
	env.Copy = func(_ context.Context, text string) (string, error) {
		got = text
		return "fake", nil
	}
	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{{ID: "s1", Msgs: []llm.Message{{Role: llm.RoleAssistant, Content: answer}}}}
	}
	if code := runCopy(context.Background(), env, []string{"answer"}); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if got != answer {
		t.Fatalf("copy is not the plain answer:\n%q", got)
	}
	for _, box := range []string{"╭", "╮", "╰", "╯", "│"} {
		if strings.Contains(got, box) {
			t.Fatalf("copy contains panel art %q", box)
		}
	}
}
