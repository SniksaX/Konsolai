package turn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/redact"
)

func TestExpandMentionsEmailNotAttach(t *testing.T) {
	cwd := t.TempDir()
	got, atts := ExpandMentions(context.Background(), cwd, "email user@example.com please")
	if len(atts) != 0 || !strings.Contains(got, "user@example.com") {
		t.Fatalf("%q %v", got, atts)
	}
}

func TestExpandMentionsMissingLeftAsText(t *testing.T) {
	cwd := t.TempDir()
	got, atts := ExpandMentions(context.Background(), cwd, "see @no-such-file.txt")
	if len(atts) != 0 || !strings.Contains(got, "@no-such-file.txt") {
		t.Fatalf("%q %v", got, atts)
	}
}

func TestExpandMentionsFileAndEscape(t *testing.T) {
	cwd := t.TempDir()
	p := filepath.Join(cwd, "main.go")
	if err := os.WriteFile(p, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, atts := ExpandMentions(context.Background(), cwd, "explain @main.go")
	if len(atts) != 1 || !strings.Contains(got, "<attached path=\"main.go\">") || !strings.Contains(got, "package main") {
		t.Fatalf("%q %+v", got, atts)
	}
	got, atts = ExpandMentions(context.Background(), cwd, `keep \@main.go literal`)
	if len(atts) != 0 || !strings.Contains(got, "@main.go") || strings.Contains(got, "<attached") {
		t.Fatalf("escape %q %v", got, atts)
	}
}

func TestExpandMentionsDirOutline(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "a.go"), []byte("package p\nfunc Hi() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, atts := ExpandMentions(context.Background(), cwd, "look @.")
	if len(atts) != 1 {
		t.Fatalf("atts %v", atts)
	}
	if !strings.Contains(got, "<attached") {
		t.Fatalf("%q", got)
	}
}

func TestExpandMentionsCapAndSensitive(t *testing.T) {
	cwd := t.TempDir()
	big := strings.Repeat("x", attachCap+100)
	if err := os.WriteFile(filepath.Join(cwd, "big.txt"), []byte(big), 0644); err != nil {
		t.Fatal(err)
	}
	got, atts := ExpandMentions(context.Background(), cwd, "@big.txt")
	if len(atts) != 1 || !strings.Contains(got, "[truncated]") {
		t.Fatalf("cap %q %v", got, atts)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("SECRET=abc123secretvalue\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !redact.SensitivePath(".env") && !redact.SensitivePath(filepath.Join(cwd, ".env")) {
		t.Skip("env not sensitive in this build")
	}
	got, atts = ExpandMentions(context.Background(), cwd, "@.env")
	if len(atts) != 1 {
		t.Fatalf("env atts %v", atts)
	}
	if strings.Contains(got, "abc123secretvalue") && strings.Contains(got, "SECRET=abc123secretvalue") {
		t.Fatalf("want masked: %q", got)
	}
}
