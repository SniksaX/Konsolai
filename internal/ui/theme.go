package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

type ColorMode uint8

const (
	ColorNone ColorMode = iota
	Color256
	ColorTrue
)

type RGB struct{ R, G, B uint8 }

type Roles struct {
	Accent, Heading, AccentDim, Meta, Muted RGB
	BannerTop, BannerBottom                 RGB
}

type role struct {
	RGB
	c256 int
	bold bool
}

type roleSet struct {
	Accent, Heading, AccentDim, Meta, Muted role
	BannerTop, BannerBottom                 role
}

type Palette struct {
	Enabled   bool
	Truecolor bool
	Scheme    string
}

var (
	themeMu    sync.Mutex
	themeForce *Palette
	schemeName = "amber"
	plainForce bool
)

var amberSet = roleSet{
	Accent:       role{RGB: RGB{0xFF, 0x9A, 0x1F}},
	Heading:      role{RGB: RGB{0xFF, 0xD2, 0x7A}, bold: true},
	AccentDim:    role{RGB: RGB{0x9A, 0x66, 0x2A}},
	Meta:         role{RGB: RGB{0xC9, 0xA2, 0x3A}},
	Muted:        role{RGB: RGB{0x7A, 0x52, 0x22}},
	BannerTop:    role{RGB: RGB{0xFF, 0xD2, 0x7A}},
	BannerBottom: role{RGB: RGB{0xFF, 0x9A, 0x1F}},
}

var violetSet = roleSet{
	Accent:       role{RGB: RGB{167, 139, 250}, c256: 141},
	Heading:      role{RGB: RGB{34, 211, 238}, c256: 96, bold: true},
	AccentDim:    role{RGB: RGB{107, 88, 168}},
	Meta:         role{RGB: RGB{180, 163, 230}},
	Muted:        role{RGB: RGB{138, 138, 138}, c256: 245},
	BannerTop:    role{RGB: RGB{211, 197, 252}},
	BannerBottom: role{RGB: RGB{167, 139, 250}},
}

// ApplyTheme selects the named palette and honors --plain for the process.
func ApplyTheme(scheme string, plain bool) {
	themeMu.Lock()
	defer themeMu.Unlock()
	if strings.EqualFold(strings.TrimSpace(scheme), "violet") {
		schemeName = "violet"
	} else {
		schemeName = "amber"
	}
	plainForce = plain
}

// Current returns the active palette.
func Current() Palette {
	themeMu.Lock()
	defer themeMu.Unlock()
	if themeForce != nil {
		p := *themeForce
		if p.Scheme == "" {
			p.Scheme = schemeName
		}
		return p
	}
	return detectPalette()
}

func detectPalette() Palette {
	scheme := schemeName
	if plainForce || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return Palette{Scheme: scheme}
	}
	if !stdoutTTY() {
		return Palette{Scheme: scheme}
	}
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	return Palette{Enabled: true, Truecolor: ct == "truecolor" || ct == "24bit", Scheme: scheme}
}

// ForceTheme overrides detection for tests.
func ForceTheme(p Palette) (restore func()) {
	themeMu.Lock()
	prev := themeForce
	cp := p
	themeForce = &cp
	themeMu.Unlock()
	return func() {
		themeMu.Lock()
		themeForce = prev
		themeMu.Unlock()
	}
}

func (p Palette) scheme() string {
	if p.Scheme == "violet" {
		return "violet"
	}
	if p.Scheme == "amber" {
		return "amber"
	}
	themeMu.Lock()
	s := schemeName
	themeMu.Unlock()
	if s == "violet" {
		return "violet"
	}
	return "amber"
}

func (p Palette) roles() roleSet {
	if p.scheme() == "violet" {
		return violetSet
	}
	return amberSet
}

func (p Palette) Roles() Roles {
	r := p.roles()
	return Roles{
		Accent: r.Accent.RGB, Heading: r.Heading.RGB,
		AccentDim: r.AccentDim.RGB, Meta: r.Meta.RGB, Muted: r.Muted.RGB,
		BannerTop: r.BannerTop.RGB, BannerBottom: r.BannerBottom.RGB,
	}
}

func (p Palette) ColorMode() ColorMode {
	if !p.Enabled {
		return ColorNone
	}
	if p.Truecolor {
		return ColorTrue
	}
	return Color256
}

func (p Palette) paint(code, s string) string {
	if !p.Enabled || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (p Palette) paintRole(r role, s string) string {
	if !p.Enabled || s == "" {
		return s
	}
	code := p.fgCode(r)
	if r.bold {
		code = "1;" + code
	}
	return p.paint(code, s)
}

func (p Palette) fgCode(r role) string {
	if p.Truecolor {
		return fmt.Sprintf("38;2;%d;%d;%d", r.R, r.G, r.B)
	}
	n := r.c256
	if n == 0 {
		n = to256(r.RGB)
	}
	return fmt.Sprintf("38;5;%d", n)
}

func (p Palette) PaintRGB(c RGB, s string) string {
	return p.paintRole(role{RGB: c}, s)
}

func (p Palette) Accent(s string) string    { return p.paintRole(p.roles().Accent, s) }
func (p Palette) Heading(s string) string   { return p.paintRole(p.roles().Heading, s) }
func (p Palette) AccentDim(s string) string { return p.paintRole(p.roles().AccentDim, s) }
func (p Palette) Meta(s string) string      { return p.paintRole(p.roles().Meta, s) }
func (p Palette) Muted(s string) string     { return p.paintRole(p.roles().Muted, s) }

func (p Palette) Strong(s string) string { return p.paint("1;97", s) }

func (p Palette) Code(s string) string {
	if p.Truecolor {
		return p.paint("38;2;245;158;11", s)
	}
	return p.paint("38;5;214", s)
}

func (p Palette) Success(s string) string { return p.paint("32", s) }

func (p Palette) Error(s string) string { return p.paint("31", s) }

func (p Palette) Warn(s string) string { return p.paint("33", s) }

func (p Palette) Link(s string) string { return p.paint("4;34", s) }

func (p Palette) Italic(s string) string { return p.paint("3", s) }

func (p Palette) AccentCode() string {
	if !p.Enabled {
		return ""
	}
	return p.fgCode(p.roles().Accent)
}

func (p Palette) MutedSeq() string {
	if !p.Enabled {
		return ""
	}
	return "\033[" + p.fgCode(p.roles().Muted) + "m"
}

func RejectCall(tool, argSummary, reason string) (callLine, rejectLine string) {
	pal := Current()
	head := tool
	if strings.TrimSpace(argSummary) != "" {
		head += " " + argSummary
	}
	return pal.Accent("▸") + " " + head, pal.Warn("✗ rejected: " + reason)
}

func to256(c RGB) int {
	q := func(v uint8) int { return (int(v)*5 + 127) / 255 }
	return 16 + 36*q(c.R) + 6*q(c.G) + q(c.B)
}
