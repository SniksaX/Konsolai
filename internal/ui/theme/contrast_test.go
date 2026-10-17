package theme

import (
	"fmt"
	"testing"
)

func TestContrast(t *testing.T) {
	darks := []RGB{hexRGB(0x232627), hexRGB(0x1E1E1E), hexRGB(0x000000)}
	lights := []RGB{hexRGB(0xFFFFFF), hexRGB(0xFAF7F0)}
	needMin := map[Role]float64{
		Heading: 4.5, Accent: 4.5, Meta: 4.5, Muted: 4.5,
		AccentDim: 4.0, BannerTop: 3.0, BannerBottom: 3.0,
	}
	var report []string
	for _, pal := range PaletteNames {
		for _, app := range []Appearance{Dark, Light} {
			bgs := darks
			if app == Light {
				bgs = lights
			}
			cols := AllRoleRGB(pal, app)
			for role, floor := range needMin {
				c := cols[role]
				for _, bg := range bgs {
					got := Contrast(c, bg)
					line := fmt.Sprintf("%s %s %s %s on %s = %.2f (need %.1f)",
						pal, app, role, c.Hex(), bg.Hex(), got, floor)
					report = append(report, line)
					if got+1e-6 < floor {
						t.Errorf("%s", line)
					}
				}
			}
			c := cols[Subtle]
			for _, bg := range bgs {
				got := Contrast(c, bg)
				line := fmt.Sprintf("%s %s subtle %s on %s = %.2f (need 1.6–3.2)",
					pal, app, c.Hex(), bg.Hex(), got)
				report = append(report, line)
				if got+1e-6 < 1.6 || got-1e-6 > 3.2 {
					t.Errorf("%s", line)
				}
			}
		}
	}
	for _, line := range report {
		t.Log(line)
	}
}

func TestStylePrimaryUncolored(t *testing.T) {
	defer Force(true, true, "amber", Dark)()
	if Style(Primary, "hi") != "hi" {
		t.Fatalf("primary %q", Style(Primary, "hi"))
	}
	if Style(Accent, "x") == "x" {
		t.Fatal("accent should paint")
	}
}

func TestStyleOffNoESC(t *testing.T) {
	defer Force(false, false, "amber", Dark)()
	for _, r := range []Role{Accent, Heading, Muted, Link} {
		if Style(r, "x") != "x" {
			t.Fatalf("%s leaked: %q", r, Style(r, "x"))
		}
	}
}
