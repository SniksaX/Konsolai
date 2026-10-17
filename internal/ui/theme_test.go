package ui

import (
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/ui/theme"
)

func TestRejectCallUsesWarn(t *testing.T) {
	restore := ForceTheme(Palette{Enabled: true, Truecolor: false, Scheme: "amber"})
	defer restore()
	call, reject := RejectCall("export_doc", "docs/OVERVIEW.pdf", "unknown field \"content\"")
	if !strings.Contains(call, "▸") || !strings.Contains(call, "export_doc") || !strings.Contains(call, "docs/OVERVIEW.pdf") {
		t.Fatalf("call %q", call)
	}
	if !strings.Contains(reject, "✗ rejected: unknown field \"content\"") {
		t.Fatalf("reject %q", reject)
	}
	if !strings.Contains(reject, "\x1b[") {
		t.Fatalf("want warn color, got %q", reject)
	}
}

func TestPalettesResolveEveryRole(t *testing.T) {
	roles := []theme.Role{theme.Accent, theme.Heading, theme.AccentDim, theme.Meta, theme.Muted, theme.BannerTop, theme.BannerBottom, theme.Success, theme.Error, theme.Warn, theme.Link}
	for _, name := range []string{"amber", "violet"} {
		restore := theme.Force(true, true, name, theme.Dark)
		for _, role := range roles {
			got := theme.Style(role, "x")
			if got == "x" || !strings.Contains(got, "\x1b[") {
				t.Fatalf("%s %s not painted: %q", name, role, got)
			}
		}
		restore()
		restore = theme.Force(false, false, name, theme.Dark)
		if theme.Style(theme.Accent, "x") != "x" {
			t.Fatalf("%s color-off leaked SGR", name)
		}
		restore()
	}
}
