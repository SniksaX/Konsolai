package theme

import (
	"fmt"
	"testing"
)

func TestContrastFloors(t *testing.T) {
	darks := []RGB{hexRGB(0x232627), hexRGB(0x1E1E1E), hexRGB(0x000000)}
	lights := []RGB{hexRGB(0xFFFFFF), hexRGB(0xFAF7F0)}
	need := map[Role]float64{
		Heading: 4.5, Accent: 4.5, Meta: 4.5, Muted: 4.5,
		AccentDim: 4.0, Subtle: 3.0,
	}
	var report []string
	for _, pal := range []string{"amber", "violet"} {
		for _, app := range []Appearance{Dark, Light} {
			bgs := darks
			if app == Light {
				bgs = lights
			}
			cols := AllRoleRGB(pal, app)
			for role, floor := range need {
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
		}
	}
	for _, line := range report {
		t.Log(line)
	}
	t.Logf("violet dark Heading=%s Accent=%s AccentDim=%s Meta=%s Muted=%s Subtle=%s BannerTop=%s BannerBottom=%s",
		roleColor("violet", Dark, Heading).Hex(),
		roleColor("violet", Dark, Accent).Hex(),
		roleColor("violet", Dark, AccentDim).Hex(),
		roleColor("violet", Dark, Meta).Hex(),
		roleColor("violet", Dark, Muted).Hex(),
		roleColor("violet", Dark, Subtle).Hex(),
		roleColor("violet", Dark, BannerTop).Hex(),
		roleColor("violet", Dark, BannerBottom).Hex(),
	)
	t.Logf("violet light Heading=%s Accent=%s AccentDim=%s Meta=%s Muted=%s Subtle=%s BannerTop=%s BannerBottom=%s",
		roleColor("violet", Light, Heading).Hex(),
		roleColor("violet", Light, Accent).Hex(),
		roleColor("violet", Light, AccentDim).Hex(),
		roleColor("violet", Light, Meta).Hex(),
		roleColor("violet", Light, Muted).Hex(),
		roleColor("violet", Light, Subtle).Hex(),
		roleColor("violet", Light, BannerTop).Hex(),
		roleColor("violet", Light, BannerBottom).Hex(),
	)
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
