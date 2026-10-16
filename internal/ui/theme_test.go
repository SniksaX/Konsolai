package ui

import (
	"strings"
	"testing"
)

func TestRejectCallUsesWarn(t *testing.T) {
	restore := ForceTheme(Palette{Enabled: true})
	defer restore()
	call, reject := RejectCall("export_doc", "docs/OVERVIEW.pdf", "unknown field \"content\"")
	if !strings.Contains(call, "▸") || !strings.Contains(call, "export_doc") || !strings.Contains(call, "docs/OVERVIEW.pdf") {
		t.Fatalf("call %q", call)
	}
	if !strings.Contains(reject, "✗ rejected: unknown field \"content\"") {
		t.Fatalf("reject %q", reject)
	}
	if !strings.Contains(reject, "\033[33m") {
		t.Fatalf("want warn color, got %q", reject)
	}
}
