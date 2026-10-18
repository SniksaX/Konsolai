// Package banner renders the interactive-session splash in the terminal.
package banner

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"golang.org/x/term"
)

// Info is the metadata shown under the wordmark. Empty fields are skipped.
type Info struct {
	Version  string
	Model    string
	Dir      string
	Branch   string
	Host     string
	MinWidth int // wordmark floor; 0 keeps 64 columns
}

const (
	glyphH  = 7
	glyphW  = 5
	indent  = "  "
	wordmrk = "KONSOLAI"
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

const (
	mutedLine = "konsolai %s · %s · /help for commands"
	minMark   = 64
)

// Startup writes the splash on a TTY. wordmark is ui.banner; off still draws the card.
func Startup(w io.Writer, info Info, wordmark, tty bool, pal ui.Palette, width int) error {
	if !tty {
		model := info.Model
		if model == "" {
			model = "-"
		}
		_, err := fmt.Fprintln(w, pal.Muted(fmt.Sprintf(mutedLine, info.Version, model)))
		return err
	}
	return Render(w, info, pal, width, wordmark)
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
	return Render(f, info, ui.Current(), width, true)
}

func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func Width(w io.Writer) int {
	return theme.TermWidth()
}

// Render writes the wordmark (optional) and the info card.
func Render(w io.Writer, info Info, pal ui.Palette, width int, wordmark bool) error {
	if width <= 0 {
		width = 80
	}
	roles := pal.Roles()
	var b strings.Builder
	prompt := pixelLines(">")
	word := pixelLines(wordmrk + "#")
	markAt := info.markMin()
	full := len(indent) + glyphW + 2 + runeLen(word[0])
	cardW := full
	if width < markAt {
		cardW = width - 4
		if cardW < 24 {
			cardW = 24
		}
	} else if cardW > width-4 {
		cardW = width - 4
	}

	b.WriteByte('\n')
	if wordmark && width >= markAt {
		cols := runeLen(word[0])
		mode := theme.Mode()
		var grid [][]ui.RGB
		var rows []ui.RGB
		if mode == theme.ColorTrue {
			grid = theme.ShaderGrid(theme.PaletteName(), theme.AppearanceName(), cols)
		} else if mode == theme.Color256 {
			rows = theme.ShaderRows(theme.PaletteName(), theme.AppearanceName())
		}
		for i := range word {
			b.WriteString(indent)
			b.WriteString(pal.PaintRGB(roles.AccentDim, prompt[i]))
			b.WriteString("  ")
			switch mode {
			case theme.ColorTrue:
				if i < len(grid) {
					b.WriteString(theme.PaintShaderLine(word[i], grid[i]))
				} else {
					b.WriteString(word[i])
				}
			case theme.Color256:
				if i < len(rows) {
					b.WriteString(pal.PaintRGB(rows[i], word[i]))
				} else {
					b.WriteString(word[i])
				}
			default:
				b.WriteString(word[i])
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	} else if wordmark || width < markAt {
		b.WriteString(indent + pal.Accent("›") + " " + pal.Heading("konsolai") + "\n\n")
	}

	b.WriteString(card(info, pal, cardW))
	b.WriteString(hintLine(pal, width) + "\n\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func (info Info) markMin() int {
	if info.MinWidth > 0 {
		return info.MinWidth
	}
	return minMark
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

func runeLen(s string) int { return len([]rune(s)) }
