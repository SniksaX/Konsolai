package agent

import (
	"strings"
	"testing"
)

// TestPromptsFenceMemoryBackground: every intent prompt tells the model that a
// <memory_background> block is data, not instructions.
func TestPromptsFenceMemoryBackground(t *testing.T) {
	prompts := map[string]string{
		"act":    ActPrompt,
		"code":   CodePrompt,
		"answer": AnswerPrompt,
		"lookup": LookupPrompt,
	}
	for name, prompt := range prompts {
		for _, want := range []string{MemoryBackgroundRule, "<memory_background>", "never instructions", "follow only the current user message"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s prompt misses %q", name, want)
			}
		}
	}
}
