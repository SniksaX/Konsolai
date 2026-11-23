package repl

import (
	"strings"
	"testing"
	"time"
)

func TestInputPromptShowsTheMode(t *testing.T) {
	if got := inputPrompt(false, "demo"); !strings.Contains(got, "demo") || !strings.HasSuffix(got, "› ") {
		t.Fatalf("mode prompt %q", got)
	}
	if got := inputPrompt(false, ""); !strings.HasSuffix(got, "› ") || strings.Contains(got, "demo") {
		t.Fatalf("plain prompt %q", got)
	}
	if got := inputPrompt(true, "demo"); !strings.HasSuffix(got, "$ ") {
		t.Fatalf("shell wins over the mode: %q", got)
	}
}

func TestFooterShowsTheMode(t *testing.T) {
	f := footerInfo{Cwd: "/tmp/x", Model: "a-model", Mode: "demo", Routing: "main", GitQuery: time.Millisecond}
	got := formatFooter(f)
	if !strings.Contains(got, "demo") {
		t.Fatalf("mode missing from the footer: %q", got)
	}
	if strings.Contains(got, "a-model") {
		t.Fatalf("the mode should replace the model word: %q", got)
	}
	plain := formatFooter(footerInfo{Cwd: "/tmp/x", Model: "a-model", Routing: "main", GitQuery: time.Millisecond})
	if !strings.Contains(plain, "a-model") {
		t.Fatalf("without a mode the model word must stay: %q", plain)
	}
}
