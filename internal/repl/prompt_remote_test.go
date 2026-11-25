package repl

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func footerPlain(f footerInfo) string {
	return theme.StripANSI(formatFooter(f))
}

func TestFooterMarksRemoteModel(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	got := footerPlain(footerInfo{Cwd: "/tmp/x", Model: "org/gpt-4o", Routing: "main", Remote: true})
	if !strings.Contains(got, "gpt-4o remote") {
		t.Fatalf("remote marker missing: %q", got)
	}
}

func TestFooterKeepsLocalModelPlain(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	got := footerPlain(footerInfo{Cwd: "/tmp/x", Model: "org/gpt-4o", Routing: "main"})
	if strings.Contains(got, "remote") {
		t.Fatalf("local model marked: %q", got)
	}
}

func TestFooterDropsRemoteMarkerWithTheModelWord(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	shell := footerPlain(footerInfo{Cwd: "/tmp/x", Model: "org/gpt-4o", Routing: "main", Shell: true})
	if strings.Contains(shell, "remote") {
		t.Fatalf("shell footer marked: %q", shell)
	}
	wide := footerPlain(footerInfo{Cwd: "/tmp/x", Model: "org/gpt-4o", Routing: "main", Remote: true})
	if !strings.Contains(wide, "remote") {
		t.Fatalf("marker lost at width 120: %q", wide)
	}
	t.Setenv("COLUMNS", "40")
	narrow := footerPlain(footerInfo{Cwd: "/tmp/x", Model: "org/gpt-4o", Routing: "main", Remote: true})
	if strings.Contains(narrow, "remote") {
		t.Fatalf("marker survived the dropped model word: %q", narrow)
	}
}
