package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

const dedupeNotePrefix = "already in context"

// TestReadDedupeTokenSaving replays the config-edit pattern (the same path read
// three times) against a fake provider and measures prompt tokens with and
// without the per-turn dedupe. "Before" is the captured request log with every
// dedupe note replaced by the content it stood for.
func TestReadDedupeTokenSaving(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 900)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d %s", i+1, strings.Repeat("payload ", 3))
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"main.go","start_line":1,"end_line":700}`)),
		assistant(call("2", "read_file", `{"path":"main.go","start_line":1,"end_line":700}`)),
		assistant(call("3", "read_file", `{"path":"main.go","start_line":1,"end_line":700}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	rep.modelLen = 200000
	if err := Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, MaxSteps: 8,
	}, "sys", "read main.go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	firstRead := rep.toolContent("1")
	if len(firstRead) < 10_000 {
		t.Fatalf("the measured read is too small: %d bytes", len(firstRead))
	}

	// Measure at the package default ratio so a fake provider's usage numbers
	// cannot skew the comparison; only the prompt chars differ before/after.
	const ratio = 0.30
	estimate := func(msgs []llm.Message, tools []llm.ToolSpec) int {
		chars := llm.MessageChars(msgs) + llm.ToolsSchemaChars(tools)
		return int(float64(chars)*ratio + 0.5)
	}
	var after, before int
	afterPer := make([]int, len(rep.reqs))
	for i := range rep.reqs {
		afterPer[i] = estimate(rep.reqs[i], rep.tools[i])
		after += afterPer[i]
		beforeMsgs := make([]llm.Message, len(rep.reqs[i]))
		copy(beforeMsgs, rep.reqs[i])
		for j, m := range beforeMsgs {
			if m.Role == llm.RoleTool && strings.HasPrefix(m.Content, dedupeNotePrefix) {
				m.Content = firstRead
				beforeMsgs[j] = m
			}
		}
		before += estimate(beforeMsgs, rep.tools[i])
	}
	t.Logf("three reads, first read %d bytes: prompt tokens before=%d after=%d saved=%d (%.1f%%)",
		len(firstRead), before, after, before-after, 100*float64(before-after)/float64(before))
	t.Logf("per-request prompt tokens after dedupe: %v (ratio %.2f)", afterPer, ratio)
	if after >= before {
		t.Fatalf("dedupe saved no prompt tokens: before=%d after=%d", before, after)
	}
	if before-after < 5_000 {
		t.Fatalf("dedupe saved too little for this payload: %d tokens", before-after)
	}
	// The dedupe notes themselves must stay short.
	for _, id := range []string{"2", "3"} {
		if note := rep.toolContent(id); len(note) > 200 {
			t.Fatalf("dedupe note %s is not short: %d bytes", id, len(note))
		}
	}
}
