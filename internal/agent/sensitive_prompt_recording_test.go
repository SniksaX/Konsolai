package agent

import (
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

// codeRunningWritePromptRecording pins the exact terminal bytes of the separate
// "runs code later" confirmation after the heading fix. It is a new recording:
// the heading block is unnumbered and the file preview starts at line 1.
const codeRunningWritePromptRecording = "\n" +
	"runs code later: write scripts/x.sh (7 B, new)\n" +
	"this file runs code later: scripts/ at the project root\n\n" +
	"    1 echo hi\n" +
	"[y]es [n]o [a]ll [q]uit  ✓ approved\n\n"

func TestSensitivePromptRecordingCodeRunningWrite(t *testing.T) {
	confirm := &layoutConfirm{}
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"scripts/x.sh","content":"echo hi"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, confirm, Config{})
	if err != nil {
		t.Fatal(err)
	}
	idx := -1
	for i, summary := range confirm.summaries {
		if stringsHas(summary, "runs code later") {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no sensitive prompt in %q", confirm.summaries)
	}
	got := renderPrompt(t, confirm.summaries[idx], confirm.previews[idx])
	if got != codeRunningWritePromptRecording {
		t.Fatalf("prompt recording changed\n got: %q\nwant: %q", got, codeRunningWritePromptRecording)
	}
}

func stringsHas(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
