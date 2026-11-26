package agent

import (
	"context"
	"encoding/json"
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

// runReadTurn runs one agent turn over root and returns the replay.
func runReadTurn(t *testing.T, root string, replies []llm.Message) *replay {
	t.Helper()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, replies)
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, MaxSteps: 8,
	}, "sys", "task")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return rep
}

func TestReadDedupeIdenticalReadReturnsShortNote(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 300)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d", i+1)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runReadTurn(t, root, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		assistant(call("2", "read_file", `{"path":"a.txt"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	first := rep.toolContent("1")
	second := rep.toolContent("2")
	if !strings.Contains(first, "line 0001") {
		t.Fatalf("the first read lost its content: %q", first)
	}
	if !strings.Contains(second, readNotePrefix) {
		t.Fatalf("the identical read did not return a note: %q", second)
	}
	if strings.Contains(second, "line 0001") || len(second) > 200 {
		t.Fatalf("the identical read still paid for the content: %q", second)
	}
	if strings.Contains(second, "line 0300") {
		t.Fatalf("the note contains file content: %q", second)
	}
}

func TestReadDedupeDifferentUncoveredRangeReturnsContent(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d", i+1)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runReadTurn(t, root, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt","start_line":1,"end_line":5}`)),
		assistant(call("2", "read_file", `{"path":"a.txt","start_line":20,"end_line":25}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	second := rep.toolContent("2")
	if strings.Contains(second, readNotePrefix) {
		t.Fatalf("an uncovered range was skipped: %q", second)
	}
	if !strings.Contains(second, "line 0020") || !strings.Contains(second, "line 0025") {
		t.Fatalf("the uncovered range lost its content: %q", second)
	}
}

func TestReadDedupeResetsEachTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReadTurn(t, root, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	second := runReadTurn(t, root, []llm.Message{
		assistant(call("1", "read_file", `{"path":"a.txt"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	})
	got := second.toolContent("1")
	if strings.Contains(got, readNotePrefix) {
		t.Fatalf("the read cache leaked across turns: %q", got)
	}
	if !strings.Contains(got, "alpha") {
		t.Fatalf("the new turn lost the file content: %q", got)
	}
}

// toolMessages decodes every recorded request body's tool messages by id.
func toolMessages(t *testing.T, bodies []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, body := range bodies {
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("decode provider body: %v", err)
		}
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool && m.ToolCallID != "" {
				out[m.ToolCallID] = m.Content
			}
		}
	}
	return out
}

func TestReadDedupeKeepsConfigMasked(t *testing.T) {
	_, reg := protectProject(t)
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"config.yaml\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"config.yaml\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	client, bodies := fakeCapture(t, replies)
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, Err: io.Discard, MaxSteps: 4, Intent: "act",
	}, "sys", "read the config twice"); err != nil {
		t.Fatal(err)
	}
	for _, body := range *bodies {
		for _, leak := range []string{"server-binary", "SECRETARG", "SECRETENV", "db.internal.example", "mcp.internal.example"} {
			if strings.Contains(body, leak) {
				t.Fatalf("raw config content %q reached the provider body", leak)
			}
		}
	}
	tm := toolMessages(t, *bodies)
	first, ok := tm["1"]
	if !ok || (!strings.Contains(first, "<set>") && !strings.Contains(first, "<masked>")) {
		t.Fatalf("the first config read was not the masked view: %q", first)
	}
	second, ok := tm["2"]
	if !ok {
		t.Fatal("the second config read has no tool reply")
	}
	if strings.Contains(second, "SECRETARG") {
		t.Fatalf("the repeated config read leaked raw content: %q", second)
	}
}

func TestReadDedupeKeepsSecretsRefused(t *testing.T) {
	_, reg := protectProject(t)
	home := os.Getenv("HOME")
	secrets := filepath.Join(home, ".config", "konsolai", "secrets")
	if err := os.WriteFile(secrets, []byte("key: "+securityTestKey()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := fmt.Sprintf(`{"path":%q}`, secrets)
	quoted, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	readCall := func(id string) string {
		return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":%q,"type":"function","function":{"name":"read_file","arguments":%s}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, id, quoted)
	}
	replies := []string{
		readCall("1"),
		readCall("2"),
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	client, bodies := fakeCapture(t, replies)
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, Err: io.Discard, MaxSteps: 6, Intent: "act",
		IdenticalRejects: 5,
	}, "sys", "read the secrets twice"); err != nil {
		t.Fatal(err)
	}
	tm := toolMessages(t, *bodies)
	for _, id := range []string{"1", "2"} {
		got, ok := tm[id]
		if !ok {
			t.Fatalf("read %s has no tool reply", id)
		}
		if !strings.Contains(got, "refus") && !strings.Contains(got, "error") {
			t.Fatalf("read %s was not refused: %q", id, got)
		}
		if strings.Contains(got, securityTestKey()) {
			t.Fatalf("read %s leaked the secret", id)
		}
	}
	for _, body := range *bodies {
		if strings.Contains(body, securityTestKey()) {
			t.Fatal("the secrets value reached a provider body")
		}
	}
}

// securityTestKey assembles a secret-shaped literal at runtime.
func securityTestKey() string { return "sk" + "-dedupe-" + "0a1b2c3d" }
