package welcome

import (
	"io"
	"strings"

	"github.com/siraj/konsolai/internal/ui"
)

func renderFrame(w io.Writer, d Data, width, height int) {
	lim := d.Limits.norm()
	if height < lim.TinyHeight || width < lim.FrameWidth {
		renderSplit(w, d, width, height)
		return
	}
	writeLines(w, frameLines(ui.Current(), d, width, height), width)
}

func frameLines(pal ui.Palette, d Data, width, height int) []string {
	lim := d.Limits.norm()
	right, paint := healthLabel(d)
	rightFn := func(s string) string { return paint(pal, s) }
	top := pal.Subtle("╭") + titleBar(pal, width-2, frameTitle(d), right, rightFn) + pal.Subtle("╮")

	lines := []string{"", top, frameRow(pal, width, "")}
	for _, row := range frameBanner(pal, width, lim.TinyWidth) {
		lines = append(lines, frameRow(pal, width, row))
	}
	lines = append(lines,
		frameRow(pal, width, ""),
		pal.Subtle("├"+strings.Repeat("─", width-2)+"┤"),
	)

	c1, c2, c3 := frameCols(width)
	recentHead := " " + pal.AccentDim("RECENT")
	if height < lim.RecentHeight {
		recentHead = ""
	}
	header := fitWidth(" "+pal.AccentDim("MODELS"), c1) +
		fitWidth(" "+pal.AccentDim("SESSION"), c2) +
		fitWidth(recentHead, c3)
	lines = append(lines, frameRow(pal, width, " "+header+" "))

	models := modelRows(pal, c1, d.Models)
	session := sessionLines(pal, c2, d.Session)
	var recent []string
	if height >= lim.RecentHeight {
		recent = insetRecent(pal, c3, d.Recent)
	}
	n := len(models)
	if len(session) > n {
		n = len(session)
	}
	if len(recent) > n {
		n = len(recent)
	}
	for i := 0; i < n; i++ {
		a, b, c := "", "", ""
		if i < len(models) {
			a = models[i]
		}
		if i < len(session) {
			b = session[i]
		}
		if i < len(recent) {
			c = recent[i]
		}
		body := fitWidth(a, c1) + fitWidth(b, c2) + fitWidth(c, c3)
		lines = append(lines, frameRow(pal, width, " "+body+" "))
	}
	lines = append(lines,
		pal.Subtle("╰"+strings.Repeat("─", width-2)+"╯"),
		"",
		hintLine(pal, width, d.Tip),
		"",
	)
	return lines
}

func frameCols(width int) (c1, c2, c3 int) {
	inner := width - 4
	if inner < 3 {
		return 1, 1, 1
	}
	c1 = inner / 3
	c2 = c1
	c3 = inner - c1 - c2
	if c1 < 1 {
		c1 = 1
	}
	if c2 < 1 {
		c2 = 1
	}
	if c3 < 1 {
		c3 = 1
	}
	return c1, c2, c3
}

func frameRow(pal ui.Palette, width int, content string) string {
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	return pal.Subtle("│") + fitWidth(content, inner) + pal.Subtle("│")
}

func frameBanner(pal ui.Palette, width, minW int) []string {
	wm := wordmarkMin(width, minW)
	for len(wm) < 4 {
		wm = append(wm, "")
	}
	if len(wm) > 4 {
		wm = wm[:4]
	}
	sides := []string{
		"Your terminal agent, on your own models.",
		modeLine(pal),
		"",
		pal.Muted("Plans before it acts. Asks before it writes."),
	}
	room := width - 67
	if room < 1 {
		room = 1
	}
	out := make([]string, 4)
	for i := 0; i < 4; i++ {
		side := ""
		if i < len(sides) {
			side = clip(sides[i], room)
		}
		out[i] = " " + fitWidth(wm[i], bannerCols) + "  " + fitWidth(side, room)
	}
	return out
}

func modeLine(pal ui.Palette) string {
	modes := []string{"answer", "lookup", "act", "build"}
	var b strings.Builder
	for i, mode := range modes {
		if i > 0 {
			b.WriteString(pal.Subtle(" · "))
		}
		b.WriteString(pal.Meta(mode))
	}
	return b.String()
}
