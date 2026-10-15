package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/llm"
)

func withStateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return filepath.Join(dir, "konsolai")
}

func TestSaveLoadLastSession(t *testing.T) {
	withStateHome(t)
	in := LastSession{
		Profile: "default",
		CWD:     "/tmp/kf",
		Title:   "hello",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "token ghp_abcdefghijklmnopqrstuvwxyz012345"},
			{Role: llm.RoleAssistant, Content: "ok"},
		},
	}
	if err := SaveLast(in); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(LastPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	got, ok := LoadLast("default", "/tmp/kf", 30*time.Minute)
	if !ok {
		t.Fatal("want load")
	}
	if got.Title != "hello" || len(got.Messages) != 2 {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(got.Messages[0].Content, "ghp_") {
		t.Fatalf("not redacted: %q", got.Messages[0].Content)
	}
	if _, ok := LoadLast("other", "/tmp/kf", 30*time.Minute); ok {
		t.Fatal("other profile")
	}
	if _, ok := LoadLast("default", "/tmp/other", 30*time.Minute); ok {
		t.Fatal("other cwd")
	}
}

func TestLoadLastIgnoresIdleAndCorrupt(t *testing.T) {
	withStateHome(t)
	if err := SaveLast(LastSession{Profile: "p", CWD: "/c", Title: "t", Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadLast("p", "/c", time.Nanosecond); ok {
		t.Fatal("idle should expire")
	}
	if err := os.WriteFile(LastPath(), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadLast("p", "/c", time.Hour); ok {
		t.Fatal("corrupt")
	}
	if err := SaveLast(LastSession{Profile: "p", CWD: "/c", Title: "new", Messages: []llm.Message{{Role: llm.RoleUser, Content: "y"}}}); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadLast("p", "/c", time.Hour)
	if !ok || got.Title != "new" {
		t.Fatalf("%+v %v", got, ok)
	}
}

func TestSaveLastCapsTwenty(t *testing.T) {
	withStateHome(t)
	var msgs []llm.Message
	for i := 0; i < 25; i++ {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "m"})
	}
	if err := SaveLast(LastSession{Profile: "p", CWD: "/c", Messages: msgs}); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadLast("p", "/c", time.Hour)
	if !ok || len(got.Messages) != 20 {
		t.Fatalf("len %d ok %v", len(got.Messages), ok)
	}
	b, err := os.ReadFile(LastPath())
	if err != nil {
		t.Fatal(err)
	}
	var raw LastSession
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Messages) != 20 {
		t.Fatalf("raw %d", len(raw.Messages))
	}
}
