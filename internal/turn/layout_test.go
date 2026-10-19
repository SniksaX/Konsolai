package turn

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestGoldenAnswerTurnLayout(t *testing.T) {
	defer ui.ForceTheme(ui.Palette{})()
	client := fakeLLM(t, []llm.Message{{Role: llm.RoleAssistant, Content: "hello there"}})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = &out
	res := r.RunTurn(context.Background(), Request{Text: "hi", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "  ◆ answer\n") {
		t.Fatalf("badge %q", got)
	}
	if !strings.Contains(got, "    hello there\n") {
		t.Fatalf("body indent %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("double blank %q", got)
	}
	if !strings.Contains(got, "\n\n    ") {
		t.Fatalf("stats blank %q", got)
	}
}

func TestGoldenActTurnLayout(t *testing.T) {
	defer ui.ForceTheme(ui.Palette{})()
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"done"}`},
		}}},
	})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = &out
	res := r.RunTurn(context.Background(), Request{Text: "do it", Intent: "act", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "  ◆ act\n") {
		t.Fatalf("badge %q", got)
	}
	if !strings.Contains(got, "    done\n") {
		t.Fatalf("body indent %q", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("double blank %q", got)
	}
}

func TestEscalatePrintsOnlyFinalBadge(t *testing.T) {
	defer ui.ForceTheme(ui.Palette{})()
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, Content: "I'll take a look at the project then create a README.md"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"created"}`},
		}}},
	})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = &out
	res := r.RunTurn(context.Background(), Request{
		Text: "what is listening", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, out.String())
	}
	got := out.String()
	if strings.Contains(got, "◆ answer") || strings.Contains(got, "· answer") {
		t.Fatalf("first badge printed %q", got)
	}
	if !strings.Contains(got, "  ◆ lookup") || !strings.Contains(got, " · escalated") {
		t.Fatalf("want final escalated badge, got %q", got)
	}
	if strings.Count(got, "◆") != 1 {
		t.Fatalf("badges %q", got)
	}
}
