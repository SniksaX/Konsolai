package agent

import (
	"context"
	"encoding/json"
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

func liveRun(t *testing.T, g *safety.Guard, replies []string, requirePlan bool) string {
	t.Helper()
	rec := &offerRecorder{replies: replies}
	var out strings.Builder
	if err := Run(context.Background(), Config{
		Client: rec.client(t), Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: &out, Err: io.Discard,
		RequirePlan: requirePlan, MaxSteps: 6, Intent: "act",
	}, "sys", "do the task"); err != nil {
		t.Fatalf("run: %v", err)
	}
	return out.String()
}

func callJSON(id, name, args string) string { return chatCall(id, name, args) }

// TestLiveStyleNamedFolderWriteCompletes: "create hello2.txt containing hi in
// <named folder>" completes with a direct write on the low-effort act path.
func TestLiveStyleNamedFolderWriteCompletes(t *testing.T) {
	project := t.TempDir()
	named := t.TempDir()
	target := filepath.Join(named, "hello2.txt")
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	g.AllowPlanFiles([]string{target})
	replies := []string{
		callJSON("1", "write_file", `{"path":`+jsonString(target)+`,"content":"hi"}`),
		callJSON("2", "finish", `{"summary":"wrote hello2.txt"}`),
	}
	out := liveRun(t, g, replies, false)
	if !strings.Contains(out, "wrote hello2.txt") {
		t.Fatalf("turn did not finish:\n%s", out)
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "hi" {
		t.Fatalf("named folder write failed: %q %v", body, err)
	}
}

// TestLiveStyleConfigCreateIsRefused: "create config.yaml containing x: 1" is
// refused by the write tool with the /set pointer, and no file is written.
func TestLiveStyleConfigCreateIsRefused(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".real"), 0o755); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(project, ".real", "config.yaml")
	if err := os.WriteFile(active, []byte("defaults: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_CONFIG", active)
	if err := os.Symlink(active, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	replies := []string{
		callJSON("1", "write_file", `{"path":"config.yaml","content":"x: 1"}`),
		callJSON("2", "finish", `{"summary":"done"}`),
	}
	out := liveRun(t, g, replies, false)
	if !strings.Contains(out, "changed only through /set") {
		t.Fatalf("config create was not refused with the /set pointer:\n%s", out)
	}
	body, _ := os.ReadFile(active)
	if string(body) != "defaults: {}\n" {
		t.Fatalf("the protected config was modified: %q", body)
	}
}

// TestLiveStyleConfigEditIsRefused: "edit config.yaml to add an mcp server" is
// refused for both edit_file and the shell route.
func TestLiveStyleConfigEditIsRefused(t *testing.T) {
	project := t.TempDir()
	active := filepath.Join(project, ".real", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("defaults: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_CONFIG", active)
	if err := os.Symlink(active, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, replies := range [][]string{
		{callJSON("1", "edit_file", `{"path":"config.yaml","old_str":"defaults: {}","new_str":"defaults:\n  mcp: {}"}`), callJSON("2", "finish", `{"summary":"done"}`)},
		{callJSON("1", "run_shell", `{"cmd":"sed -i s/x/y/ config.yaml"}`), callJSON("2", "finish", `{"summary":"done"}`)},
	} {
		out := liveRun(t, g, replies, false)
		if !strings.Contains(out, "changed only through /set") {
			t.Fatalf("config edit route was not refused with /set:\n%s", out)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var _ = llm.Message{}
