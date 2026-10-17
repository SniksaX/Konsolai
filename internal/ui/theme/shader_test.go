package theme

import (
	"testing"
)

func TestShader(t *testing.T) {
	for _, pal := range PaletteNames {
		for _, app := range []Appearance{Dark, Light} {
			top := roleColor(pal, app, BannerTop)
			bot := roleColor(pal, app, BannerBottom)
			d := OKLabDist(RGBToOKLab(top), RGBToOKLab(bot))
			if d+1e-9 < 0.12 {
				t.Errorf("%s %s BannerTop/Bottom OKLab dist %.4f < 0.12", pal, app, d)
			}
		}
	}
}

func TestNoGrey(t *testing.T) {
	roles := []Role{BannerTop, BannerBottom, Heading, Accent, AccentDim, Meta, Muted}
	for _, pal := range PaletteNames {
		if pal == "mono" {
			continue
		}
		for _, app := range []Appearance{Dark, Light} {
			for _, role := range roles {
				c := roleColor(pal, app, role)
				ch := Chroma(c)
				if ch+1e-9 < 0.02 {
					t.Errorf("%s %s %s chroma %.4f < 0.02 hex %s", pal, app, role, ch, c.Hex())
				}
			}
		}
	}
}

func TestOKLabRoundTrip(t *testing.T) {
	colors := []RGB{
		hexRGB(0x000000), hexRGB(0xFFFFFF), hexRGB(0xFF0000), hexRGB(0x00FF00),
		hexRGB(0x0000FF), hexRGB(0xFFE29A), hexRGB(0x4CC9F0), hexRGB(0xB388FF),
		hexRGB(0x3DDC97), hexRGB(0xFF6FA0), hexRGB(0x808080),
	}
	for _, c := range colors {
		back := OKLabToRGB(RGBToOKLab(c))
		if abs8(back.R, c.R) > 1 || abs8(back.G, c.G) > 1 || abs8(back.B, c.B) > 1 {
			t.Errorf("roundtrip %s -> %s", c.Hex(), back.Hex())
		}
	}
	a, b := hexRGB(0xFFE29A), hexRGB(0xFF8A1F)
	if MixOKLab(a, b, 0) != a {
		t.Fatalf("mix t=0 %s", MixOKLab(a, b, 0).Hex())
	}
	if MixOKLab(a, b, 1) != b {
		t.Fatalf("mix t=1 %s", MixOKLab(a, b, 1).Hex())
	}
}

func abs8(a, b uint8) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}
