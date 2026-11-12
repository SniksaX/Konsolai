package commands

import (
	"context"
	"testing"
)

// mentionMode offers two rows and nothing for any other prefix.
type mentionMode struct {
	on bool
}

func (m *mentionMode) Label() string                                    { return "mentions" }
func (m *mentionMode) Active() bool                                     { return m.on }
func (m *mentionMode) Handle(_ context.Context, _ *Env, _ string) error { return nil }
func (m *mentionMode) Mentions(prefix string) []Choice {
	if prefix == "" || prefix == "al" {
		return []Choice{{Value: "all", Description: "everyone"}, {Value: "alpha", Description: "the first"}}
	}
	return nil
}

func TestMentionProviderIsOptional(t *testing.T) {
	m := &mentionMode{on: true}
	var mode InputMode = m
	p, ok := mode.(MentionProvider)
	if !ok {
		t.Fatal("the mode should provide mentions")
	}
	if got := p.Mentions("al"); len(got) != 2 || got[0].Value != "all" {
		t.Fatalf("mentions %v", got)
	}
	if got := p.Mentions("zz"); got != nil {
		t.Fatalf("an empty list means not mine: %v", got)
	}
	plain := &fakeMode{label: "plain"}
	if _, ok := InputMode(plain).(MentionProvider); ok {
		t.Fatal("a plain mode must not provide mentions")
	}
}
