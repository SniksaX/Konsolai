package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// scriptedKeys feeds a fixed key sequence to the real Prompter and counts the
// reads so a bypass (auto-answer without reading) is observable.
type scriptedKeys struct {
	mu    sync.Mutex
	keys  []byte
	reads int
}

func (k *scriptedKeys) read() (byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.reads++
	if len(k.keys) == 0 {
		return 0, io.EOF
	}
	b := k.keys[0]
	k.keys = k.keys[1:]
	return b, nil
}

func (k *scriptedKeys) readCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reads
}

// realPrompter installs the scripted key reader and returns the prompter and
// the bytes it writes, exactly like the REPL's terminal adapter.
func realPrompter(t *testing.T, keys string) (*ui.Prompter, *bytes.Buffer, *scriptedKeys) {
	t.Helper()
	reader := &scriptedKeys{keys: []byte(keys)}
	ui.SetKeyReader(reader.read)
	t.Cleanup(func() { ui.SetKeyReader(nil) })
	buf := &bytes.Buffer{}
	return ui.NewPrompter(buf, false), buf, reader
}

func planWriteScript() llm.Message {
	args := `{"summary":"write a script","steps":["write scripts/x.sh"],"files":["scripts/x.sh"],"commands":[]}`
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("p1", "propose_plan", args)}}
}

func writeScript() llm.Message {
	args := `{"path":"scripts/x.sh","content":"echo hi"}`
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("w1", "write_file", args)}}
}

func finishMessage() llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("f1", "finish", `{"summary":"done"}`)}}
}

// TestRealPrompterApproveAllStillAsksSensitiveWrite is the bug repro: a real
// Prompter set to approve-all by the plan must still ask the separate
// code-running question, and "n" must leave the file unwritten.
func TestRealPrompterApproveAllStillAsksSensitiveWrite(t *testing.T) {
	dir := t.TempDir()
	client := fakeLLM(t, []llm.Message{planWriteScript(), writeScript(), finishMessage()})
	prompt, promptOut, keys := realPrompter(t, "an")
	r := testRunner(t, client, prompt)
	res := r.RunTurn(context.Background(), Request{Text: "write a script", Intent: "build", Cwd: dir, New: true})
	_ = res
	text := promptOut.String()
	if !strings.Contains(text, "runs code later") {
		t.Fatalf("no separate code-running prompt was shown; keys read %d, prompt %q", keys.readCount(), text)
	}
	if _, err := os.Stat(filepath.Join(dir, "scripts", "x.sh")); !os.IsNotExist(err) {
		t.Fatalf("rejected sensitive write was written (err=%v)", err)
	}
}

func TestRealPrompterApproveAllApprovesSensitiveWithY(t *testing.T) {
	dir := t.TempDir()
	client := fakeLLM(t, []llm.Message{planWriteScript(), writeScript(), finishMessage()})
	prompt, promptOut, _ := realPrompter(t, "ay")
	r := testRunner(t, client, prompt)
	r.RunTurn(context.Background(), Request{Text: "write a script", Intent: "build", Cwd: dir, New: true})
	if !strings.Contains(promptOut.String(), "runs code later") {
		t.Fatalf("no separate code-running prompt: %q", promptOut.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "scripts", "x.sh"))
	if err != nil || string(b) != "echo hi" {
		t.Fatalf("approved write %q %v", b, err)
	}
}

// TestRealPrompterPackageJSONSensitiveWrite keeps the package.json scripts case
// on the same separate prompt after an approve-all plan.
func TestRealPrompterPackageJSONSensitiveWrite(t *testing.T) {
	dir := t.TempDir()
	old := `{"scripts":{"build":"tsc"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	planArgs, _ := json.Marshal(map[string]any{
		"summary": "change package scripts", "steps": []string{"edit package.json"},
		"files": []string{"package.json"}, "commands": []string{},
	})
	newContent := `{"scripts":{"build":"deploy"}}` + "\n"
	writeArgs, _ := json.Marshal(map[string]string{"path": "package.json", "content": newContent})
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("p1", "propose_plan", string(planArgs))}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("w1", "write_file", string(writeArgs))}},
		finishMessage(),
	})
	prompt, promptOut, _ := realPrompter(t, "an")
	r := testRunner(t, client, prompt)
	r.RunTurn(context.Background(), Request{Text: "change package scripts", Intent: "build", Cwd: dir, New: true})
	if !strings.Contains(promptOut.String(), "runs code later") || !strings.Contains(promptOut.String(), "package.json") {
		t.Fatalf("no package.json sensitive prompt: %q", promptOut.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || string(b) != old {
		t.Fatalf("rejected package.json write changed the file: %q %v", b, err)
	}
}

// TestRealPrompterShellRedirectSensitiveWrite keeps the Makefile redirect on
// the same separate prompt after an approve-all plan.
func TestRealPrompterShellRedirectSensitiveWrite(t *testing.T) {
	dir := t.TempDir()
	cmd := `printf 'all:\n' > Makefile`
	plan := `{"summary":"write Makefile","steps":["redirect"],"files":["Makefile"],"commands":[` + strconvJSON(cmd) + `]}`
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	client := fakeLLM(t, []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("p1", "propose_plan", plan)}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("s1", "run_shell", string(args))}},
		finishMessage(),
	})
	prompt, promptOut, _ := realPrompter(t, "an")
	r := testRunner(t, client, prompt)
	r.RunTurn(context.Background(), Request{Text: "write Makefile", Intent: "build", Cwd: dir, New: true})
	if !strings.Contains(promptOut.String(), "runs code later") || !strings.Contains(promptOut.String(), "Makefile") {
		t.Fatalf("no Makefile sensitive prompt: %q", promptOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); !os.IsNotExist(err) {
		t.Fatalf("rejected shell redirect wrote Makefile (err=%v)", err)
	}
}

func strconvJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
