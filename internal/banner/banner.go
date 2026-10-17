// Package banner renders the interactive-session splash in the terminal.
package banner

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/siraj/konsolai/internal/ui"
	"golang.org/x/term"
)

// Info is the metadata shown under the wordmark. Empty fields are skipped.
type Info struct {
	Version string
	Model   string
	Dir     string
}

const (
	glyphH  = 7
	glyphW  = 5
	indent  = "  "
	wordmrk = "KONSOLAI"
	hint    = "/ for commands   ! for shell   @ for files"
)

var font = map[rune][glyphH]string{
	'>': {"10000", "01000", "00100", "00010", "00100", "01000", "10000"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"11111", "00100", "00100", "00100", "00100", "00100", "11111"},
	'#': {"11111", "11111", "11111", "11111", "11111", "11111", "11111"},
}

const mutedLine = "konsolai %s · %s · /help for commands"

// Startup writes the pixel splash when enabled on a TTY, otherwise the one-line banner.
func Startup(w io.Writer, info Info, enabled, tty bool, pal ui.Palette, width int) error {
	if !enabled || !tty {
		model := info.Model
		if model == "" {
			model = "-"
		}
		_, err := fmt.Fprintln(w, pal.Muted(fmt.Sprintf(mutedLine, info.Version, model)))
		return err
	}
	return Render(w, info, pal, width)
}

// RenderTTY sizes the splash from the file's terminal and the active theme.
func RenderTTY(f *os.File, info Info) error {
	if f == nil {
		return nil
	}
	width := 80
	if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
		width = w
	}
	return Render(f, info, ui.Current(), width)
}

func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func Width(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if n, _, err := term.GetSize(int(f.Fd())); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

// Render writes the wordmark using the theme palette. Color-off skips SGR.
func Render(w io.Writer, info Info, pal ui.Palette, width int) error {
	if width <= 0 {
		width = 80
	}
	roles := pal.Roles()
	var b strings.Builder
	prompt := pixelLines(">")
	word := pixelLines(wordmrk + "#")
	full := len(indent) + glyphW + 2 + runeLen(word[0])

	b.WriteByte('\n')
	if width >= full {
		for i := range word {
			b.WriteString(indent)
			b.WriteString(pal.PaintRGB(roles.AccentDim, prompt[i]))
			b.WriteString("  ")
			b.WriteString(pal.PaintRGB(lerp(roles.BannerTop, roles.BannerBottom, i, len(word)), word[i]))
			b.WriteByte('\n')
		}
	} else {
		b.WriteString(indent + pal.PaintRGB(roles.AccentDim, "> ") + pal.PaintRGB(roles.BannerBottom, "konsolai") + "\n")
	}

	if meta := joinNonEmpty(info.Version, info.Model, info.Dir); meta != "" {
		b.WriteString("\n" + indent + pal.PaintRGB(roles.Meta, meta) + "\n")
	}
	b.WriteString(indent + pal.PaintRGB(roles.Muted, hint) + "\n\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func pixelLines(text string) []string {
	rows := make([]strings.Builder, (glyphH+1)/2)
	first := true
	for _, ch := range text {
		if !first {
			for i := range rows {
				rows[i].WriteByte(' ')
			}
		}
		first = false
		if ch == ' ' {
			for i := range rows {
				rows[i].WriteString(strings.Repeat(" ", glyphW-1))
			}
			continue
		}
		g, ok := font[ch]
		if !ok {
			continue
		}
		for i := range rows {
			top, bot := 2*i, 2*i+1
			for c := 0; c < glyphW; c++ {
				u := g[top][c] == '1'
				d := bot < glyphH && g[bot][c] == '1'
				switch {
				case u && d:
					rows[i].WriteRune('█')
				case u:
					rows[i].WriteRune('▀')
				case d:
					rows[i].WriteRune('▄')
				default:
					rows[i].WriteByte(' ')
				}
			}
		}
	}
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = rows[i].String()
	}
	return out
}

func lerp(a, b ui.RGB, i, n int) ui.RGB {
	if n <= 1 {
		return a
	}
	t := func(x, y uint8) uint8 { return uint8(int(x) + (int(y)-int(x))*i/(n-1)) }
	return ui.RGB{R: t(a.R, b.R), G: t(a.G, b.G), B: t(a.B, b.B)}
}

func joinNonEmpty(parts ...string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "   ")
}

func runeLen(s string) int { return len([]rune(s)) }
