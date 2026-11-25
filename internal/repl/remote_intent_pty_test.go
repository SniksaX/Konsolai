package repl

import (
	"os"
	"strings"
	"testing"
)

// TestRemoteGatePerIntent drives the call-time remote confirmation for all
// four intents on a real PTY: the question is visible under the spinner, y
// proceeds, n refuses, and a bare Enter re-asks instead of declining.
func TestRemoteGatePerIntent(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "remote-intents" {
		remoteConfirmChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	for _, intent := range []string{"answer", "lookup", "act", "build"} {
		t.Run(intent, func(t *testing.T) {
			t.Setenv("KONSOLAI_REMOTE_INTENT", intent)
			t.Run("yes proceeds", func(t *testing.T) {
				r := startRemoteConfirmPTY(t, "TestRemoteGatePerIntent", "remote-intents")
				r.ask(t, "hi there\r")
				// Give the spinner several ticks to overdraw the prompt if the
				// pause guard regressed.
				waitPromptVisible(t, r)
				if intent == "answer" {
					r.v.typeKeys("y\r", screenHas("REMOTE_OK"))
				} else {
					r.v.typeKeys("y\r", nil)
					waitRemoteRequests(t, r, 1)
				}
				r.exit(t)
			})
			t.Run("n refuses", func(t *testing.T) {
				r := startRemoteConfirmPTY(t, "TestRemoteGatePerIntent", "remote-intents")
				r.ask(t, "hi there\r")
				waitPromptVisible(t, r)
				r.v.typeKeys("n\r", screenHas("remote use declined"))
				r.noRequests(t, " after a typed n")
				r.exit(t)
			})
			t.Run("enter asks again", func(t *testing.T) {
				r := startRemoteConfirmPTY(t, "TestRemoteGatePerIntent", "remote-intents")
				r.ask(t, "hi there\r")
				waitPromptVisible(t, r)
				before := strings.Count(string(r.v.raw()), remoteRemotePrompt)
				r.v.typeKeys("\r", func(sc *screen) bool {
					return strings.Count(string(r.v.raw()), remoteRemotePrompt) > before
				})
				r.v.typeKeys("n\r", screenHas("remote use declined"))
				r.noRequests(t, " after the re-asked and declined prompt")
				r.exit(t)
			})
		})
	}
}

// waitPromptVisible proves the question stays on screen while the spinner is
// running, and that no spinner frame sits on the prompt's row.
func waitPromptVisible(t *testing.T, r *remoteConfirmView) {
	t.Helper()
	sc := r.v.expect("remote question under the spinner", screenHas(remoteRemotePrompt))
	for _, line := range sc.lines() {
		if strings.Contains(line, remoteRemotePrompt) {
			if strings.ContainsAny(line, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
				failScreen(t, "the spinner is drawn over the remote question", sc, r.v.raw())
			}
			return
		}
	}
	failScreen(t, "the remote question line disappeared", sc, r.v.raw())
}
