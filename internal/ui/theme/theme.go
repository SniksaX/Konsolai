package theme

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

type Role string

const (
	Primary      Role = "primary"
	Heading      Role = "heading"
	Accent       Role = "accent"
	AccentDim    Role = "accentdim"
	Meta         Role = "meta"
	Muted        Role = "muted"
	Subtle       Role = "subtle"
	BannerTop    Role = "bannertop"
	BannerBottom Role = "bannerbottom"
	Success      Role = "success"
	Error        Role = "error"
	Warn         Role = "warn"
	Link         Role = "link"
)

type ColorMode uint8

const (
	ColorNone ColorMode = iota
	Color256
	ColorTrue
)

type Appearance string

const (
	Dark  Appearance = "dark"
	Light Appearance = "light"
	Auto  Appearance = "auto"
)

type Config struct {
	Palette       string
	Appearance    Appearance
	Plain         bool
	CodeHighlight bool
}

type snapshot struct {
	enabled   bool
	truecolor bool
	palette   string
	appear    Appearance
	codeOn    bool
	plain     bool
}

var (
	mu      sync.Mutex
	forced  *snapshot
	current = snapshot{palette: "amber", appear: Dark, codeOn: true}
)

type roleRGB struct{ Dark, Light RGB }

// hueRoles is one theme: eight roles per appearance. Success/Error/Warn/Link
// stay global.
type hueRoles struct {
	BannerTop, BannerBottom, Heading, Accent, AccentDim, Meta, Muted, Subtle roleRGB
}

func hue(darkTop, darkBot, darkHead, darkAcc, darkDim, darkMeta, darkMuted, darkSubtle,
	lightTop, lightBot, lightHead, lightAcc, lightDim, lightMeta, lightMuted, lightSubtle uint32) hueRoles {
	return hueRoles{
		BannerTop:    roleRGB{hexRGB(darkTop), hexRGB(lightTop)},
		BannerBottom: roleRGB{hexRGB(darkBot), hexRGB(lightBot)},
		Heading:      roleRGB{hexRGB(darkHead), hexRGB(lightHead)},
		Accent:       roleRGB{hexRGB(darkAcc), hexRGB(lightAcc)},
		AccentDim:    roleRGB{hexRGB(darkDim), hexRGB(lightDim)},
		Meta:         roleRGB{hexRGB(darkMeta), hexRGB(lightMeta)},
		Muted:        roleRGB{hexRGB(darkMuted), hexRGB(lightMuted)},
		Subtle:       roleRGB{hexRGB(darkSubtle), hexRGB(lightSubtle)},
	}
}

var themes = map[string]hueRoles{
	"amber": hue(
		0xFFE29A, 0xFF8A1F, 0xFFE0A3, 0xFFA033, 0xD98A35, 0xEBC27E, 0xB8966A, 0x5C4A33,
		0xC46A00, 0x8A3B00, 0x6E3A00, 0xAA5400, 0x9A5A14, 0x7E531C, 0x7A6148, 0xCDB89A,
	),
	"glacier": hue(
		0xE0FBFF, 0x3D8BFF, 0xD6F6FF, 0x4CC9F0, 0x3FA3CC, 0x9ADCF0, 0x86A9B8, 0x34505C,
		0x0A86B0, 0x1B4FC4, 0x0B3F5C, 0x0B78A8, 0x1B6A8F, 0x245E78, 0x55707D, 0xA9C2CD,
	),
	"nebula": hue(
		0xFFD9F2, 0x8F5CFF, 0xEEDCFF, 0xB388FF, 0x9470D9, 0xD2B8F7, 0xA396BF, 0x51466B,
		0xB2399A, 0x5B2BC7, 0x3E1F78, 0x6A3BD1, 0x6A4AAE, 0x5A3F8C, 0x6E6385, 0xC4B7DC,
	),
	"jade": hue(
		0xEFFFB8, 0x1FC7A0, 0xDDFBE6, 0x3DDC97, 0x36AC7C, 0xA3E8C2, 0x8DAF9C, 0x2F5242,
		0x4E8A00, 0x007A63, 0x0F4A33, 0x0B7A52, 0x2A7356, 0x2E6249, 0x58715F, 0xA9C7B5,
	),
	"rose": hue(
		0xFFE0C7, 0xFF4F8B, 0xFFE1EA, 0xFF6FA0, 0xD65C86, 0xFFB8CB, 0xBC97A3, 0x664452,
		0xC2410C, 0xB0145A, 0x6B1036, 0xB8215F, 0x9E3A60, 0x8A3557, 0x7D5F6B, 0xD6B4C2,
	),
	// Mono keeps the previous steel-blue / grey set (exempt from no-grey).
	// Subtle L was nudged so contrast stays in 1.6–3.2 (see Phase 2 report).
	"mono": hue(
		0x98B6E1, 0x5E8ED0, 0x98B6E1, 0x5E8ED0, 0x6485B3, 0xA6A6A6, 0x8C8C8C, 0x5D5D5D,
		0x28518B, 0x1B375F, 0x1B375F, 0x28518B, 0x32517C, 0x535353, 0x666666, 0x909090,
	),
}

var sharedRoles = map[Role]roleRGB{
	Success: {hexRGB(0x7BD88F), hexRGB(0x1E7B34)},
	Error:   {hexRGB(0xFF6B6B), hexRGB(0xC62828)},
	Warn:    {hexRGB(0xFFD166), hexRGB(0x9A6700)},
	Link:    {hexRGB(0x6CB6FF), hexRGB(0x0B63CE)},
}

func Apply(c Config) {
	mu.Lock()
	defer mu.Unlock()
	current.palette = normalizePalette(c.Palette)
	current.plain = c.Plain
	current.codeOn = c.CodeHighlight
	switch c.Appearance {
	case Light, Dark:
		current.appear = c.Appearance
	default:
		current.appear = Auto
	}
	current.enabled, current.truecolor = detectMode(c.Plain)
}

func detectMode(plain bool) (enabled, truecolor bool) {
	if plain || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false, false
	}
	if !stdoutTTY() {
		return false, false
	}
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	return true, ct == "truecolor" || ct == "24bit"
}

func stdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// PaletteNames is the set of ui.theme values, amber first (default).
var PaletteNames = []string{"amber", "glacier", "nebula", "jade", "rose", "mono"}

var themeAliases = map[string]string{
	"amber":   "amber",
	"glacier": "glacier",
	"cyan":    "glacier",
	"ocean":   "glacier",
	"nebula":  "nebula",
	"violet":  "nebula",
	"jade":    "jade",
	"green":   "jade",
	"forest":  "jade",
	"rose":    "rose",
	"red":     "rose",
	"mono":    "mono",
}

func KnownPalette(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := themeAliases[s]; ok {
		return n, true
	}
	return "", false
}

func NormalizePalette(s string) string {
	if n, ok := KnownPalette(s); ok {
		return n
	}
	return "amber"
}

func normalizePalette(s string) string { return NormalizePalette(s) }

func paletteRoles(name string) map[Role]roleRGB {
	h, ok := themes[NormalizePalette(name)]
	if !ok {
		h = themes["amber"]
	}
	out := map[Role]roleRGB{
		BannerTop: h.BannerTop, BannerBottom: h.BannerBottom,
		Heading: h.Heading, Accent: h.Accent, AccentDim: h.AccentDim,
		Meta: h.Meta, Muted: h.Muted, Subtle: h.Subtle,
	}
	for r, pair := range sharedRoles {
		out[r] = pair
	}
	return out
}

func snap() snapshot {
	mu.Lock()
	if forced != nil {
		s := *forced
		mu.Unlock()
		return s
	}
	s := current
	once, set := appearOnce, appearSet
	mu.Unlock()
	if s.appear == Auto || s.appear == "" {
		if set && once != "" {
			s.appear = once
		} else {
			s.appear = fromCOLORFGBG()
		}
	}
	s.enabled, s.truecolor = detectMode(s.plain)
	return s
}

func Force(enabled, truecolor bool, palette string, appear Appearance) (restore func()) {
	mu.Lock()
	prev := forced
	cp := snapshot{enabled: enabled, truecolor: truecolor, palette: normalizePalette(palette), appear: appear, codeOn: true}
	forced = &cp
	mu.Unlock()
	return func() {
		mu.Lock()
		forced = prev
		mu.Unlock()
	}
}

func Enabled() bool {
	return snap().enabled
}

func Plain() bool { return snap().plain }

func Mode() ColorMode {
	s := snap()
	if !s.enabled {
		return ColorNone
	}
	if s.truecolor {
		return ColorTrue
	}
	return Color256
}

func PaletteName() string { return snap().palette }

func AppearanceName() Appearance {
	s := snap()
	if s.appear == Auto || s.appear == "" {
		return resolvedAppear()
	}
	return s.appear
}

func CodeHighlight() bool { return snap().codeOn && snap().enabled }

func RoleRGB(role Role) RGB {
	s := snap()
	return roleColor(s.palette, s.appear, role)
}

func roleColor(palette string, appear Appearance, role Role) RGB {
	if appear == Auto || appear == "" {
		appear = Dark
	}
	set := paletteRoles(palette)
	pair, ok := set[role]
	if !ok {
		return RGB{}
	}
	if appear == Light {
		return pair.Light
	}
	return pair.Dark
}

func AllRoleRGB(palette string, appear Appearance) map[Role]RGB {
	set := paletteRoles(palette)
	out := make(map[Role]RGB, len(set))
	for r := range set {
		out[r] = roleColor(palette, appear, r)
	}
	return out
}

// Style paints text with role. Primary is never colored. Link is underlined.
func Style(role Role, text string) string {
	if text == "" {
		return text
	}
	if role == Primary {
		return text
	}
	s := snap()
	if !s.enabled {
		return text
	}
	c := roleColor(s.palette, s.appear, role)
	code := fgCode(s, c)
	if role == Heading {
		code = "1;" + code
	}
	if role == Link {
		code = "4;" + code
	}
	return wrap(code, text)
}

func Bold(text string) string {
	if text == "" || !snap().enabled {
		return text
	}
	return wrap("1", text)
}

func Italic(text string) string {
	if text == "" || !snap().enabled {
		return text
	}
	return wrap("3", text)
}

func Underline(text string) string {
	if text == "" || !snap().enabled {
		return text
	}
	return wrap("4", text)
}

func PaintRGB(c RGB, text string) string {
	if text == "" || !snap().enabled {
		return text
	}
	return wrap(fgCode(snap(), c), text)
}

func fgCode(s snapshot, c RGB) string {
	if s.truecolor {
		return fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B)
	}
	return fmt.Sprintf("38;5;%d", to256(c))
}

func wrap(code, s string) string {
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func AccentCode() string {
	s := snap()
	if !s.enabled {
		return ""
	}
	return fgCode(s, roleColor(s.palette, s.appear, Accent))
}

func HeadingCode() string {
	s := snap()
	if !s.enabled {
		return ""
	}
	return "1;" + fgCode(s, roleColor(s.palette, s.appear, Heading))
}

func MutedSeq() string {
	s := snap()
	if !s.enabled {
		return ""
	}
	return "\x1b[" + fgCode(s, roleColor(s.palette, s.appear, Muted)) + "m"
}

func DescStyle() string {
	return MutedSeq()
}
