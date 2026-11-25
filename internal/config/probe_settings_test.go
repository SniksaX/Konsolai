package config

import (
	"strings"
	"testing"
	"time"
)

func TestRemoteProbeTimeoutDefault(t *testing.T) {
	b := Builtin()
	if b.LLM.RemoteProbeTimeoutMS != 3000 || b.RemoteProbeTimeout() != 3*time.Second {
		t.Fatalf("builtin remote probe %d %s", b.LLM.RemoteProbeTimeoutMS, b.RemoteProbeTimeout())
	}
	// A zero Config still reads the documented default.
	var zero Config
	if zero.RemoteProbeTimeout() != 3*time.Second {
		t.Fatalf("zero remote probe %s", zero.RemoteProbeTimeout())
	}
	k, ok := KeyByPath("llm.remote_probe_timeout_ms")
	if !ok {
		t.Fatal("llm.remote_probe_timeout_ms is not registered")
	}
	if k.Apply != ApplyNext {
		t.Fatalf("remote probe apply %v", k.Apply)
	}
}

func TestAnswerMainAboveTokensDefault(t *testing.T) {
	b := Builtin()
	if b.Router.AnswerMainAboveTokens != 200 {
		t.Fatalf("router.answer_main_above_tokens %d", b.Router.AnswerMainAboveTokens)
	}
	k, ok := KeyByPath("router.answer_main_above_tokens")
	if !ok {
		t.Fatal("router.answer_main_above_tokens is not registered")
	}
	if k.Apply != ApplyNext || k.Check != CheckNonNeg {
		t.Fatalf("key apply %v check %v", k.Apply, k.Check)
	}
	c := Builtin()
	c.Router.AnswerMainAboveTokens = -1
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "router.answer_main_above_tokens") {
		t.Fatalf("negative threshold error %v", err)
	}
}
