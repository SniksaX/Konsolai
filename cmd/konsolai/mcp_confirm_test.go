package main

import (
	"strings"
	"testing"
)

// TestConfirmMCPPrivateAnswers covers the y/n reading and the prompt wording:
// only the server name is shown, never a configured env or header value.
func TestConfirmMCPPrivateAnswers(t *testing.T) {
	cases := []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" yes \n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},
		{"", false},
		{"maybe\n", false},
	}
	for _, tc := range cases {
		var out strings.Builder
		ok, err := confirmMCPPrivate("web", true, strings.NewReader(tc.answer), &out)
		if err != nil {
			t.Fatalf("answer %q: %v", tc.answer, err)
		}
		if ok != tc.want {
			t.Fatalf("answer %q: got %v, want %v", tc.answer, ok, tc.want)
		}
		prompt := out.String()
		if !strings.HasPrefix(prompt, "this sends MCP results from web to the remote model [y/N]: ") {
			t.Fatalf("prompt %q", prompt)
		}
		if strings.Contains(prompt, "Authorization") || strings.Contains(prompt, "${") {
			t.Fatalf("prompt prints configured data: %q", prompt)
		}
	}
}

// TestConfirmMCPPrivateNonInteractiveRefuses proves a non-interactive run never
// prompts and never allows a private server, so only --yes can allow one.
func TestConfirmMCPPrivateNonInteractiveRefuses(t *testing.T) {
	var out strings.Builder
	ok, err := confirmMCPPrivate("web", false, strings.NewReader("y\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a non-interactive run allowed the private server")
	}
	if out.Len() != 0 {
		t.Fatalf("a non-interactive run printed a prompt: %q", out.String())
	}
}

// TestMCPPrivatePromptRefusesWithoutTTY checks the wired entrypoint helper: in
// this test process stdin is not a terminal, so it refuses without reading.
func TestMCPPrivatePromptRefusesWithoutTTY(t *testing.T) {
	saved := stdinIsTTY
	t.Cleanup(func() { stdinIsTTY = saved })
	stdinIsTTY = func() bool { return false }
	ok, err := mcpPrivatePrompt("web")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("the prompt allowed a private server without a TTY")
	}
}
