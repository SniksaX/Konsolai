package turn

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/sysctx"
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
