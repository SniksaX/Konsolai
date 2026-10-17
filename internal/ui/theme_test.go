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

func TestPalettesResolveEveryRole(t *testing.T) {
	roles := []string{"Accent", "Heading", "AccentDim", "Meta", "Muted", "BannerTop", "BannerBottom", "Success", "Error", "Warn", "Link"}
	for _, name := range []string{"amber", "violet"} {
		p := Palette{Enabled: true, Truecolor: true, Scheme: name}
		rs := p.Roles()
		if rs.Accent == (RGB{}) || rs.Heading == (RGB{}) || rs.BannerTop == (RGB{}) || rs.BannerBottom == (RGB{}) {
			t.Fatalf("%s missing RGB: %+v", name, rs)
		}
		for _, role := range roles {
			got := paintNamed(p, role, "x")
			if got == "x" || !strings.Contains(got, "\033[") {
				t.Fatalf("%s %s not painted: %q", name, role, got)
			}
		}
		off := Palette{Scheme: name}
		if off.Accent("x") != "x" || off.PaintRGB(rs.BannerTop, "x") != "x" {
			t.Fatalf("%s color-off leaked SGR", name)
		}
	}
}

func paintNamed(p Palette, role, s string) string {
	switch role {
	case "Accent":
		return p.Accent(s)
	case "Heading":
		return p.Heading(s)
	case "AccentDim":
		return p.AccentDim(s)
	case "Meta":
		return p.Meta(s)
	case "Muted":
		return p.Muted(s)
	case "BannerTop":
		return p.PaintRGB(p.Roles().BannerTop, s)
	case "BannerBottom":
		return p.PaintRGB(p.Roles().BannerBottom, s)
	case "Success":
		return p.Success(s)
	case "Error":
		return p.Error(s)
	case "Warn":
		return p.Warn(s)
	case "Link":
		return p.Link(s)
	default:
		return s
	}
}
