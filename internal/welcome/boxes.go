package welcome

import (
	"strings"

	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
)

func drawBox(pal ui.Palette, width int, title, right string, rightPaint func(string) string, rows []string) []string {
	if width < 2 {
		width = 2
	}
	inner := width - 2
	if len(rows) == 0 {
		rows = []string{""}
	}
	out := make([]string, 0, len(rows)+2)
	out = append(out, pal.Subtle("╭")+titleBar(pal, inner, title, right, rightPaint)+pal.Subtle("╮"))
	for _, row := range rows {
		out = append(out, pal.Subtle("│")+fitWidth(row, inner)+pal.Subtle("│"))
	}
	out = append(out, pal.Subtle("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

func titleBar(pal ui.Palette, inner int, title, right string, rightPaint func(string) string) string {
	if inner <= 0 {
		return ""
	}
	if strings.TrimSpace(title) == "" && right == "" {
		return pal.Subtle(strings.Repeat("─", inner))
	}
	if rightPaint == nil {
		rightPaint = pal.Muted
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "konsolai"
	}
	if right == "" {
		title = truncatePad(title, max(1, inner-4))
		tw := theme.DisplayWidth(title)
		dashes := inner - (2 + tw + 1)
		if dashes < 1 {
			title = theme.Truncate(title, max(1, inner-3))
			tw = theme.DisplayWidth(title)
			dashes = inner - (2 + tw + 1)
		}
		if dashes < 0 {
			dashes = 0
		}
		return pal.Subtle("─ ") + pal.Heading(title) + pal.Subtle(" "+strings.Repeat("─", dashes))
	}
	// "─ " + title + " " + dashes + " " + right + " ─" == inner
	title = theme.Truncate(title, max(1, inner-8))
	right = theme.Truncate(right, max(1, inner/2))
	tw := theme.DisplayWidth(title)
	rw := theme.DisplayWidth(right)
	dashes := inner - tw - rw - 6
	for dashes < 1 && tw > 1 {
		title = theme.Truncate(title, tw-1)
		tw = theme.DisplayWidth(title)
		dashes = inner - tw - rw - 6
	}
	for dashes < 1 && rw > 1 {
		right = theme.Truncate(right, rw-1)
		rw = theme.DisplayWidth(right)
		dashes = inner - tw - rw - 6
	}
	if dashes < 1 {
		dashes = 1
	}
	line := pal.Subtle("─ ") + pal.Heading(title) + pal.Subtle(" "+strings.Repeat("─", dashes)+" ") + rightPaint(right) + pal.Subtle(" ─")
	got := theme.DisplayWidth(line)
	if got < inner {
		dashes += inner - got
		line = pal.Subtle("─ ") + pal.Heading(title) + pal.Subtle(" "+strings.Repeat("─", dashes)+" ") + rightPaint(right) + pal.Subtle(" ─")
	}
	return clip(line, inner)
}

func frameTitle(d Data) string {
	v := strings.TrimSpace(d.Version)
	if v == "" {
		return "konsolai"
	}
	return "konsolai " + v
}

func healthLabel(d Data) (string, func(ui.Palette, string) string) {
	host := hostName(d)
	switch healthOf(d) {
	case healthOnline:
		return "● " + host + " online", func(p ui.Palette, s string) string { return p.Success(s) }
	case healthOffline:
		return "○ " + host + " offline", func(p ui.Palette, s string) string { return p.Error(s) }
	default:
		return "● " + host + " degraded", func(p ui.Palette, s string) string { return p.Warn(s) }
	}
}

func placePair(left, right []string, width int) []string {
	bw := (width - 6) / 2
	if bw < 2 {
		bw = 2
	}
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		switch {
		case l != "" && r != "":
			out[i] = clip("  "+fitWidth(l, bw)+"  "+r, width)
		case l != "":
			out[i] = clip("  "+l, width)
		default:
			out[i] = clip(spaces(2+bw+2)+r, width)
		}
	}
	return out
}

func joinBanner(left, right []string, width, boxW int) []string {
	gap := width - bannerCols - boxW
	if gap < 1 {
		gap = 1
	}
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = clip(fitWidth(l, bannerCols)+spaces(gap)+r, width)
	}
	return out
}
