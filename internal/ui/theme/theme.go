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

var amberRoles = map[Role]roleRGB{
	Heading:      {hexRGB(0xFFD27A), hexRGB(0x7A4300)},
	Accent:       {hexRGB(0xFF9A1F), hexRGB(0xB35900)},
	AccentDim:    {hexRGB(0xC77E2E), hexRGB(0x9A5A14)},
	Meta:         {hexRGB(0xE6B566), hexRGB(0x8A5A1C)},
	Muted:        {hexRGB(0xB08A5C), hexRGB(0x82654A)},
	Subtle:       {hexRGB(0x8C6E4A), hexRGB(0xA28C74)},
	BannerTop:    {hexRGB(0xFFD27A), hexRGB(0xB35900)},
	BannerBottom: {hexRGB(0xFF9A1F), hexRGB(0x7A4300)},
	Success:      {hexRGB(0x7BD88F), hexRGB(0x1E7B34)},
	Error:        {hexRGB(0xFF6B6B), hexRGB(0xC62828)},
	Warn:         {hexRGB(0xFFD166), hexRGB(0x9A6700)},
	Link:         {hexRGB(0x6CB6FF), hexRGB(0x0B63CE)},
}

// Violet at the same HSL lightness as amber; hue 270. Dark values are the
// lightest that still clear the WCAG floors on #232627/#1E1E1E/#000000.
var violetRoles = map[Role]roleRGB{
	Heading:      {hexRGB(0xB266FF), hexRGB(0x3D007A)},
	Accent:       {hexRGB(0xB06DF3), hexRGB(0x5C00B8)},
	AccentDim:    {hexRGB(0xA16DD5), hexRGB(0x57149A)},
	Meta:         {hexRGB(0xAB74E2), hexRGB(0x531C8A)},
	Muted:        {hexRGB(0xA180C2), hexRGB(0x664A82)},
	Subtle:       {hexRGB(0x8562A7), hexRGB(0x917AA8)},
	BannerTop:    {hexRGB(0xB266FF), hexRGB(0x5C00B8)},
	BannerBottom: {hexRGB(0xB06DF3), hexRGB(0x3D007A)},
	Success:      {hexRGB(0x7BD88F), hexRGB(0x1E7B34)},
	Error:        {hexRGB(0xFF6B6B), hexRGB(0xC62828)},
	Warn:         {hexRGB(0xFFD166), hexRGB(0x9A6700)},
	Link:         {hexRGB(0x6CB6FF), hexRGB(0x0B63CE)},
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

func normalizePalette(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "violet") {
		return "violet"
	}
	return "amber"
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
	set := amberRoles
	if palette == "violet" {
		set = violetRoles
	}
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
	set := amberRoles
	if palette == "violet" {
		set = violetRoles
	}
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
