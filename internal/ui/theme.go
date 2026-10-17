package ui

import (
	"strings"

	"github.com/siraj/konsolai/internal/ui/theme"
)

type ColorMode = theme.ColorMode

const (
	ColorNone = theme.ColorNone
	Color256  = theme.Color256
	ColorTrue = theme.ColorTrue
)

type RGB = theme.RGB

type Roles struct {
	Accent, Heading, AccentDim, Meta, Muted RGB
	BannerTop, BannerBottom                 RGB
	Subtle                                  RGB
}

type Palette struct {
	Enabled   bool
	Truecolor bool
	Scheme    string
}

func ApplyTheme(scheme string, plain bool) {
	theme.Apply(theme.Config{Palette: scheme, Appearance: theme.Auto, Plain: plain, CodeHighlight: true})
}

func ApplyUI(palette, appearance string, plain, codeHighlight bool) {
	ApplyUIOpts(palette, appearance, plain, codeHighlight, true)
}

func ApplyUIOpts(palette, appearance string, plain, codeHighlight, stripEmoji bool) {
	app := theme.Appearance(strings.ToLower(strings.TrimSpace(appearance)))
	if app != theme.Light && app != theme.Dark {
		app = theme.Auto
	}
	theme.Apply(theme.Config{Palette: palette, Appearance: app, Plain: plain, CodeHighlight: codeHighlight})
	SetStripEmoji(stripEmoji)
}

var stripEmoji = true

func SetStripEmoji(on bool) (restore func()) {
	prev := stripEmoji
	stripEmoji = on
	return func() { stripEmoji = prev }
}

func StripEmojiOn() bool { return stripEmoji }

func Current() Palette {
	return Palette{
		Enabled:   theme.Enabled(),
		Truecolor: theme.Mode() == theme.ColorTrue,
		Scheme:    theme.PaletteName(),
	}
}

func ForceTheme(p Palette) (restore func()) {
	appear := theme.Dark
	return theme.Force(p.Enabled, p.Truecolor, p.Scheme, appear)
}

func (p Palette) ColorMode() ColorMode { return theme.Mode() }

func (p Palette) Roles() Roles {
	return Roles{
		Accent: theme.RoleRGB(theme.Accent), Heading: theme.RoleRGB(theme.Heading),
		AccentDim: theme.RoleRGB(theme.AccentDim), Meta: theme.RoleRGB(theme.Meta),
		Muted: theme.RoleRGB(theme.Muted), Subtle: theme.RoleRGB(theme.Subtle),
		BannerTop: theme.RoleRGB(theme.BannerTop), BannerBottom: theme.RoleRGB(theme.BannerBottom),
	}
}

func (p Palette) Accent(s string) string    { return theme.Style(theme.Accent, s) }
func (p Palette) Heading(s string) string   { return theme.Style(theme.Heading, s) }
func (p Palette) AccentDim(s string) string { return theme.Style(theme.AccentDim, s) }
func (p Palette) Meta(s string) string      { return theme.Style(theme.Meta, s) }
func (p Palette) Muted(s string) string     { return theme.Style(theme.Muted, s) }
func (p Palette) Subtle(s string) string    { return theme.Style(theme.Subtle, s) }
func (p Palette) Success(s string) string   { return theme.Style(theme.Success, s) }
func (p Palette) Error(s string) string     { return theme.Style(theme.Error, s) }
func (p Palette) Warn(s string) string      { return theme.Style(theme.Warn, s) }
func (p Palette) Link(s string) string      { return theme.Style(theme.Link, s) }
func (p Palette) Strong(s string) string    { return theme.Bold(s) }
func (p Palette) Italic(s string) string    { return theme.Italic(s) }
func (p Palette) Code(s string) string      { return theme.Style(theme.Accent, s) }
func (p Palette) PaintRGB(c RGB, s string) string {
	return theme.PaintRGB(c, s)
}
func (p Palette) AccentCode() string { return theme.AccentCode() }
func (p Palette) MutedSeq() string   { return theme.MutedSeq() }

func RejectCall(tool, argSummary, reason string) (callLine, rejectLine string) {
	return FormatToolCall(tool, argSummary),
		theme.Style(theme.Warn, "✗ rejected: "+reason)
}

// FormatToolCall paints "▸ name args" for a tool invocation line.
func FormatToolCall(name, args string) string {
	line := theme.Style(theme.Accent, "▸") + " " + theme.Style(theme.Primary, name)
	if strings.TrimSpace(args) != "" {
		line += " " + theme.Style(theme.Meta, args)
	}
	return line
}

// FormatToolSummary splits "name rest" and paints it as a tool call line.
func FormatToolSummary(summary string) string {
	name, args, ok := strings.Cut(strings.TrimSpace(summary), " ")
	if !ok {
		return FormatToolCall(name, "")
	}
	return FormatToolCall(name, args)
}
