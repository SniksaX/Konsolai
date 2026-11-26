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

// readDedupeEnv builds one project with a file of n lines and a registry.
func readDedupeEnv(t *testing.T, name string, lines []string) (string, *tools.Registry) {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, tools.NewRegistry(g, io.Discard, nil, nil, nil)
}

// TestReadDedupeDoesNotSkipTruncatedTail pins that a read which was cut by the
// line or byte cap records only the lines it showed, so a later read of the
// unseen tail still returns content.
func TestReadDedupeDoesNotSkipTruncatedTail(t *testing.T) {
	lines := make([]string, 2600)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d", i+1)
	}
	root, reg := readDedupeEnv(t, "big.txt", lines)
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"big.txt"}`)),
		assistant(call("2", "read_file", `{"path":"big.txt","start_line":2300,"end_line":2305}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, MaxSteps: 6,
	}, "sys", "task")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	second := rep.toolContent("2")
	if strings.Contains(second, "already in context") {
		t.Fatalf("an unseen tail was skipped as already read: %q", second)
	}
	for _, want := range []string{"line 2300", "line 2305"} {
		if !strings.Contains(second, want) {
			t.Fatalf("tail read is missing %q: %q", want, second)
		}
	}
	_ = root
}

// TestReadDedupeByteCapTail is the same guard for a read cut by the byte cap.
func TestReadDedupeByteCapTail(t *testing.T) {
	line := "abcdefghijklmnopqrstuvwxyz0123456789"
	lines := make([]string, 1200)
	for i := range lines {
		lines[i] = fmt.Sprintf("%04d %s", i+1, line)
	}
	_, reg := readDedupeEnv(t, "wide.txt", lines)
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "read_file", `{"path":"wide.txt"}`)),
		assistant(call("2", "read_file", `{"path":"wide.txt","start_line":950,"end_line":960}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, MaxSteps: 6,
	}, "sys", "task")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	second := rep.toolContent("2")
	if strings.Contains(second, "already in context") {
		t.Fatalf("a line past the byte cap was skipped as already read: %q", second)
	}
	if !strings.Contains(second, "0950") {
		t.Fatalf("tail read is missing line 950: %q", second)
	}
}

// toolContent returns the last tool reply recorded for one call id.
func (r *replay) toolContent(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := ""
	for _, msgs := range r.reqs {
		for _, m := range msgs {
			if m.Role == llm.RoleTool && m.ToolCallID == id {
				out = m.Content
			}
		}
	}
	return out
}
