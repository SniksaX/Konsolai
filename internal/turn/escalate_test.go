package turn

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/sysctx"
)

func TestRunAnswerDiscardsPromise(t *testing.T) {
	client := fakeLLM(t, []llm.Message{{
		Role: llm.RoleAssistant, Content: "I'll take a look at the project then create a README.md",
	}})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Profile = config.Builtin()
	r.Profile.Name = "default"
	code, answer, _, _, esc := r.runAnswer(context.Background(), client, client, sysctx.Snapshot{}, nil, nil,
		"good can, you create me a README.md for this project ?", agent.AnswerPrompt, false,
		Request{Plain: true}, intent.Decision{Intent: intent.Answer}, 0)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if esc != intent.Build {
		t.Fatalf("esc %q answer %q", esc, answer)
	}
	if strings.Contains(out.String(), "I'll take a look") {
		t.Fatalf("printed discarded reply: %q", out.String())
	}
}

func TestRunAnswerEscalateRerunsAsAct(t *testing.T) {
	client := fakeLLM(t, []llm.Message{
		{
			Role:    llm.RoleAssistant,
			Content: "Sure, I'll check the port now.",
			ToolCalls: []llm.ToolCall{{
				ID: "1", Type: "function",
				Function: llm.FunctionCall{Name: "escalate", Arguments: `{"intent":"act","reason":"kill a process"}`},
			}},
		},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "2", Type: "function",
			Function: llm.FunctionCall{Name: "propose_plan", Arguments: `{"summary":"kill it","steps":["stop the process"],"commands":["true"]}`},
		}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "3", Type: "function", Function: llm.FunctionCall{Name: "run_shell", Arguments: `{"cmd":"true"}`},
		}}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "4", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"checked"}`},
		}}},
	})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = &out
	r.Profile = config.Builtin()
	r.Profile.Name = "default"
	r.Profile.UI.Badge = true
	res := r.RunTurn(context.Background(), Request{
		Text: "okey kill it", Intent: "answer", Yes: true, Plain: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d %s", res.ExitCode, out.String())
	}
	if res.Intent != "act" {
		t.Fatalf("intent %q", res.Intent)
	}
	got := out.String()
	if strings.Contains(got, "I'll check the port") || strings.Contains(got, "Sure, I'll") {
		t.Fatalf("printed escalate text: %q", got)
	}
	if !strings.Contains(got, "act") || !strings.Contains(got, "escalated") {
		t.Fatalf("missing escalate badge: %q", got)
	}
	if !strings.Contains(got, "checked") {
		t.Fatalf("act finish missing: %q", got)
	}
}

func TestRunAnswerDiscardsActionClaim(t *testing.T) {
	client := fakeLLM(t, []llm.Message{{
		Role: llm.RoleAssistant, Content: "Done. Process killed. Port 10101 is now free.",
	}})
	var out bytes.Buffer
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Profile = config.Builtin()
	r.Profile.Name = "default"
	code, answer, _, _, esc := r.runAnswer(context.Background(), client, client, sysctx.Snapshot{}, nil, nil,
		"okey delete it", agent.AnswerPrompt, false,
		Request{Plain: true}, intent.Decision{Intent: intent.Answer}, 0)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if esc != intent.Act {
		t.Fatalf("esc %q answer %q", esc, answer)
	}
	if strings.Contains(out.String(), "Process killed") || strings.Contains(out.String(), "now free") {
		t.Fatalf("printed claim: %q", out.String())
	}
}

func TestContinueAfterPromise(t *testing.T) {
	r := &Runner{
		lastPromise:  true,
		lastUser:     "good can, you create me a README.md for this project ?",
		lastEscalate: intent.Build,
		lastLang:     "en",
	}
	dec, ok := r.continueDecision("continue")
	if !ok || dec.Intent != intent.Build || dec.Reason != "continue-promise" {
		t.Fatalf("%+v ok=%v", dec, ok)
	}
	dec, ok = r.continueDecision("vas-y")
	if !ok || dec.Intent != intent.Build {
		t.Fatalf("vas-y %+v", dec)
	}
}

func TestContinueDecisionClarification(t *testing.T) {
	r := &Runner{
		lastIntent:    intent.Act,
		lastLang:      "en",
		lastEffort:    intent.EffortLow,
		lastAssistant: "Which branch — prod or staging?",
	}
	dec, ok := r.continueDecision("yes first one")
	if !ok || dec.Intent != intent.Act || dec.Reason != "clarification" {
		t.Fatalf("%+v ok=%v", dec, ok)
	}
	dec, ok = r.continueDecision("le premier")
	if !ok || dec.Intent != intent.Act {
		t.Fatalf("le premier %+v", dec)
	}
}
