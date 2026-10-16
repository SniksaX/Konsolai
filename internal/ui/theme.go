package ui

import (
	"os"
	"strings"
	"sync"
)

type Palette struct {
	Enabled   bool
	Truecolor bool
}

var (
	themeMu    sync.Mutex
	themeForce *Palette
)

// Current returns the active palette.
func Current() Palette {
	themeMu.Lock()
	defer themeMu.Unlock()
	if themeForce != nil {
		return *themeForce
	}
	return detectPalette()
}

func detectPalette() Palette {
	if os.Getenv("NO_COLOR") != "" {
		return Palette{}
	}
	if !stdoutTTY() {
		return Palette{}
	}
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	return Palette{Enabled: true, Truecolor: ct == "truecolor" || ct == "24bit"}
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

func (p Palette) paint(code, s string) string {
	if !p.Enabled || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (p Palette) Accent(s string) string {
	if p.Truecolor {
		return p.paint("38;2;167;139;250", s)
	}
	return p.paint("38;5;141", s)
}

func (p Palette) Heading(s string) string {
	if p.Truecolor {
		return p.paint("1;38;2;34;211;238", s)
	}
	return p.paint("1;96", s)
}

func (p Palette) Strong(s string) string { return p.paint("1;97", s) }

func (p Palette) Code(s string) string {
	if p.Truecolor {
		return p.paint("38;2;245;158;11", s)
	}
	return p.paint("38;5;214", s)
}

func (p Palette) Muted(s string) string { return p.paint("38;5;245", s) }

func (p Palette) Success(s string) string { return p.paint("32", s) }

func (p Palette) Error(s string) string { return p.paint("31", s) }

func (p Palette) Warn(s string) string { return p.paint("33", s) }

func RejectCall(tool, argSummary, reason string) (callLine, rejectLine string) {
	pal := Current()
	head := tool
	if strings.TrimSpace(argSummary) != "" {
		head += " " + argSummary
	}
	return pal.Accent("▸") + " " + head, pal.Warn("✗ rejected: " + reason)
}

func (p Palette) Link(s string) string { return p.paint("4;34", s) }

func (p Palette) Italic(s string) string { return p.paint("3", s) }
