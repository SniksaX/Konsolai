package turn

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/ui"
)

func TestClarificationTranscriptKeepsAct(t *testing.T) {
	defer ui.ForceTheme(ui.Palette{})()
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{
				Name: "finish", Arguments: `{"summary":"Which branch — prod or staging?"}`,
			},
		}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "2", Type: "function", Function: llm.FunctionCall{
				Name: "finish", Arguments: `{"summary":"I would run git checkout prod"}`,
			},
		}}},
	})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = &out
	cwd := t.TempDir()
	res := r.RunTurn(context.Background(), Request{Text: "i'd like you to switch to prod", Intent: "act", Yes: true, Cwd: cwd, New: true})
	if res.ExitCode != 0 {
		t.Fatalf("first %d %s", res.ExitCode, out.String())
	}
	out.Reset()
	res = r.RunTurn(context.Background(), Request{Text: "yes first one", Yes: true, Cwd: cwd})
	if res.ExitCode != 0 {
		t.Fatalf("second %d %s", res.ExitCode, out.String())
	}
	if res.Intent != "act" {
		t.Fatalf("intent %s (router lowered)", res.Intent)
	}
	if !strings.Contains(res.Answer, "git checkout") && !strings.Contains(out.String(), "git checkout") {
		t.Fatalf("want checkout in follow-up %q answer=%q", out.String(), res.Answer)
	}
}

func TestLookupEscalatesToActOnce(t *testing.T) {
	defer ui.ForceTheme(ui.Palette{})()
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "1", Type: "function", Function: llm.FunctionCall{
				Name: "run_shell", Arguments: `{"cmd":"git checkout prod"}`,
			},
		}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "2", Type: "function", Function: llm.FunctionCall{
				Name: "finish", Arguments: `{"summary":"I will check out prod after you confirm."}`,
			},
		}}},
	})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = &out
	res := r.RunTurn(context.Background(), Request{Text: "switch to prod", Intent: "lookup", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, out.String())
	}
	got := out.String()
	if strings.Contains(got, "lookup can only run read-only commands") {
		t.Fatalf("leaked lookup reject %q", got)
	}
	if !strings.Contains(got, "◆ act") || !strings.Contains(got, " · escalated") {
		t.Fatalf("want act escalated, got %q", got)
	}
	if strings.Count(got, " · escalated") != 1 {
		t.Fatalf("escalated more than once %q", got)
	}
	if res.Intent != "act" && res.Intent != "lookup" {
		t.Fatalf("intent %s", res.Intent)
	}
}
