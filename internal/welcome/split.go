package welcome

import (
	"fmt"
	"io"

	"github.com/SniksaX/Konsolai/internal/banner"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func renderSplit(w io.Writer, d Data, width, height int) {
	pal := ui.Current()
	lim := ResolveLimits(d.Limits)
	var lines []string
	switch {
	case height < lim.TinyHeight:
		lines = tinyLines(pal, d, width, false)
	case width < lim.TinyWidth:
		lines = tinyLines(pal, d, width, true)
	case width < lim.NarrowWidth:
		lines = narrowLines(pal, d, width, height)
	default:
		lines = splitWide(pal, d, width, height)
	}
	writeLines(w, lines, width)
}

func tinyLines(pal ui.Palette, d Data, width int, withHint bool) []string {
	lines := []string{""}
	lines = append(lines, clip(banner.Compact(bannerInfo(d), pal), width))
	lines = append(lines, statusStrip(pal, d, width))
	if withHint {
		lines = append(lines, hintLine(pal, width, d.Tip))
	}
	lines = append(lines, "")
	return lines
}

func narrowLines(pal ui.Palette, d Data, width, height int) []string {
	lim := ResolveLimits(d.Limits)
	lines := []string{""}
	lines = append(lines, wordmarkMin(width, lim.TinyWidth)...)
	lines = append(lines, "")
	lines = append(lines, statusStrip(pal, d, width))
	if height >= lim.RecentHeight {
		lines = append(lines, "")
		contentW := width - 2
		if contentW < 8 {
			contentW = width
		}
		if len(d.Recent) == 0 {
			lines = append(lines, clip("  "+pal.Muted("no sessions yet"), width))
		} else {
			n := len(d.Recent)
			if n > 3 {
				n = 3
			}
			for _, row := range recentLines(pal, contentW, d.Recent[:n], 0) {
				lines = append(lines, clip("  "+row, width))
			}
		}
	}
	lines = append(lines, "")
	lines = append(lines, hintLine(pal, width, d.Tip))
	lines = append(lines, "")
	return lines
}

func splitWide(pal ui.Palette, d Data, width, height int) []string {
	lim := ResolveLimits(d.Limits)
	left := append(wordmarkMin(width, lim.TinyWidth), taglineLine(pal))
	boxW := width - 68
	if boxW < 16 {
		boxW = 16
	}
	host := drawBox(pal, boxW, hostName(d), fmt.Sprintf("%d/%d up", d.Up, d.Total), pal.Muted, modelRows(pal, boxW-2, d.Models))
	top := joinBanner(left, host, width, boxW)

	bw := (width - 6) / 2
	if bw < 16 {
		bw = 16
	}
	inner := bw - 2
	sessRows := sessionLines(pal, inner, d.Session)
	var bottom []string
	if height >= lim.RecentHeight {
		recW := inner - 1
		if recW < 8 {
			recW = inner
		}
		recRows := insetRecent(pal, recW, d.Recent)
		sess := drawBox(pal, bw, "session", "", nil, sessRows)
		rec := drawBox(pal, bw, "recent", "/resume", pal.Muted, recRows)
		bottom = placePair(sess, rec, width)
	} else {
		sess := drawBox(pal, bw, "session", "", nil, sessRows)
		bottom = make([]string, len(sess))
		for i, ln := range sess {
			bottom[i] = clip("  "+ln, width)
		}
	}

	lines := []string{""}
	lines = append(lines, top...)
	lines = append(lines, "")
	lines = append(lines, bottom...)
	lines = append(lines, "")
	lines = append(lines, hintLine(pal, width, d.Tip))
	lines = append(lines, "")
	return lines
}
