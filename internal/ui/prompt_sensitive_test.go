package ui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

type promptKeys struct {
	mu    sync.Mutex
	keys  []byte
	reads int
}

func (k *promptKeys) read() (byte, error) {
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

func (k *promptKeys) readCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.reads
}

func promptWithKeys(t *testing.T, out *bytes.Buffer, autoApprove bool, keys string) (*Prompter, *promptKeys) {
	t.Helper()
	restoreTheme := ForceTheme(Palette{})
	t.Cleanup(restoreTheme)
	reader := &promptKeys{keys: []byte(keys)}
	SetKeyReader(reader.read)
	t.Cleanup(func() { SetKeyReader(nil) })
	return NewPrompter(out, autoApprove), reader
}

// TestConfirmSensitiveIgnoresApproveAllAndAutoApprove proves the code-running
// question always reads a key: approve-all and --yes cannot skip it.
func TestConfirmSensitiveIgnoresApproveAllAndAutoApprove(t *testing.T) {
	var out bytes.Buffer
	p, keys := promptWithKeys(t, &out, false, "an")
	if d, _, err := p.Confirm("plan", ""); err != nil || d != ApproveAll {
		t.Fatalf("plan confirm %v %v", d, err)
	}
	if d, _, err := p.ConfirmSensitive("sensitive heading", "file content"); err != nil || d != Reject {
		t.Fatalf("sensitive confirm %v %v", d, err)
	}
	if keys.readCount() != 2 {
		t.Fatalf("sensitive prompt did not read a key: reads=%d", keys.readCount())
	}
	if got := strings.Count(out.String(), "PromptOptions"); got != 0 {
		_ = got
	}
	if strings.Count(out.String(), "[y]es [n]o [a]ll [q]uit") != 2 {
		t.Fatalf("want two prompts, got %q", out.String())
	}
	if !strings.Contains(out.String(), "sensitive heading") || !strings.Contains(out.String(), "file content") {
		t.Fatalf("sensitive prompt missing: %q", out.String())
	}

	out.Reset()
	p, keys = promptWithKeys(t, &out, true, "y")
	if d, _, err := p.ConfirmSensitive("sensitive heading", "file content"); err != nil || d != Approve {
		t.Fatalf("auto-approve sensitive %v %v", d, err)
	}
	if keys.readCount() != 1 {
		t.Fatalf("auto-approve skipped the key read: reads=%d", keys.readCount())
	}
	if !strings.Contains(out.String(), "sensitive heading") {
		t.Fatalf("auto-approve did not show the prompt: %q", out.String())
	}
}

// TestConfirmForcedIgnoresApproveAll covers the outside-plan path: forced
// questions prompt again after an approve-all plan and require a key.
func TestConfirmForcedIgnoresApproveAll(t *testing.T) {
	var out bytes.Buffer
	p, keys := promptWithKeys(t, &out, false, "an")
	if d, _, err := p.Confirm("plan", ""); err != nil || d != ApproveAll {
		t.Fatalf("plan confirm %v %v", d, err)
	}
	if d, _, err := p.ConfirmForced("forced heading", "preview"); err != nil || d != Reject {
		t.Fatalf("forced confirm %v %v", d, err)
	}
	if keys.readCount() != 2 {
		t.Fatalf("forced prompt did not read a key: reads=%d", keys.readCount())
	}
	if strings.Count(out.String(), "[y]es [n]o [a]ll [q]uit") != 2 {
		t.Fatalf("want two prompts, got %q", out.String())
	}
	if !strings.Contains(out.String(), "forced heading") {
		t.Fatalf("forced prompt missing: %q", out.String())
	}

	out.Reset()
	p, keys = promptWithKeys(t, &out, false, "")
	if _, _, err := p.ConfirmForced("forced heading", "preview"); err == nil {
		t.Fatal("forced prompt auto-answered without a key")
	}
	if keys.readCount() == 0 {
		t.Fatal("forced prompt returned without reading a key")
	}
}
