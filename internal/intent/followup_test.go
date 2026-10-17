package intent

import (
	"strings"
	"testing"
)

func TestHoldIntentOptionReplies(t *testing.T) {
	prev := "Which branch — prod or staging?"
	for _, msg := range []string{"yes first one", "the first one", "option 2", "oui", "le premier"} {
		if !HoldIntent(Act, prev, msg) {
			t.Fatalf("should hold act for %q", msg)
		}
	}
	if HoldIntent(Answer, prev, "yes") {
		t.Fatal("answer should not hold")
	}
	if HoldIntent(Act, "all done.", "yes") {
		t.Fatal("no question should not hold")
	}
	if HoldIntent(Act, prev, "please rewrite the whole module from scratch and also add tests plus docs") {
		t.Fatal("long new request should not hold")
	}
}

func TestHoldIntentShortFollowUp(t *testing.T) {
	prev := "Do you want me to check out prod?"
	if !HoldIntent(Act, prev, "go ahead") {
		t.Fatal("short follow-up")
	}
	if WordCount("yes first one") != 3 {
		t.Fatalf("words %d", WordCount("yes first one"))
	}
}

func TestWithPreviousExchange(t *testing.T) {
	got := WithPreviousExchange("yes first one", "switch to prod", "prod or staging?")
	for _, p := range []string{"yes first one", "switch to prod", "prod or staging?", "<previous_exchange>"} {
		if !strings.Contains(got, p) {
			t.Fatalf("missing %q in %q", p, got)
		}
	}
}
