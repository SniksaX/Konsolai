package agent

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// layoutConfirm records the sensitive prompt arguments and approves the write.
type layoutConfirm struct {
	summaries []string
	previews  []string
}

func (c *layoutConfirm) Confirm(summary, preview string) (ui.Decision, string, error) {
	c.summaries = append(c.summaries, summary)
	c.previews = append(c.previews, preview)
	return ui.Approve, "", nil
}

func (c *layoutConfirm) ConfirmForced(summary, preview string) (ui.Decision, string, error) {
	return c.Confirm(summary, preview)
}

// renderPrompt runs the real terminal prompter over the arguments the agent
// passed, with a single approving key, and returns the exact bytes.
func renderPrompt(t *testing.T, summary, preview string) string {
	t.Helper()
	restoreTheme := ui.ForceTheme(ui.Palette{})
	defer restoreTheme()
	keys := []byte{'y'}
	ui.SetKeyReader(func() (byte, error) {
		if len(keys) == 0 {
			return 0, io.EOF
		}
		b := keys[0]
		keys = keys[1:]
		return b, nil
	})
	defer ui.SetKeyReader(nil)

	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldOut }()
	p, err := ui.OpenPrompter(false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, _, err := p.Confirm(summary, preview); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdout = oldOut
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(raw)
}

// TestCodeRunningPromptPutsHeadingAboveNumberedContent is the bug 6 repro: the
// sensitive reason must be a heading above a blank line, and only the file
// preview may be numbered.
func TestCodeRunningPromptPutsHeadingAboveNumberedContent(t *testing.T) {
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
		if strings.Contains(summary, "runs code later") {
			idx = i
			break
		}
	}
	if idx < 0 || idx >= len(confirm.previews) {
		t.Fatalf("sensitive prompts summaries=%q previews=%q", confirm.summaries, confirm.previews)
	}
	want := "\nruns code later: write scripts/x.sh (7 B, new)\n" +
		"this file runs code later: scripts/ at the project root\n\n" +
		"    1 echo hi\n" +
		"[y]es [n]o [a]ll [q]uit  ✓ approved\n\n"
	if got := renderPrompt(t, confirm.summaries[idx], confirm.previews[idx]); got != want {
		t.Fatalf("sensitive prompt layout differs\n got: %q\nwant: %q", got, want)
	}
	if _, err := os.Stat("scripts/x.sh"); err == nil {
		t.Fatal("test cwd unexpectedly has scripts/x.sh")
	}
	_ = context.Background()
}
