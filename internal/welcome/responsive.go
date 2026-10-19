package welcome

import (
	"bytes"
	"io"
	"strings"

	"github.com/SniksaX/Konsolai/internal/banner"
	"github.com/SniksaX/Konsolai/internal/ui"
)

const (
	bannerCols = 62
	markIndent = 9 // column where the KONSOLAI blocks begin
)

// Render writes the welcome screen. layout is "split" (the default), "frame",
// or "minimal". Any other value, including empty, uses split. A zero Data is safe.
func Render(w io.Writer, d Data, layout string, width, height int) {
	if w == nil {
		return
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 40
	}
	switch strings.TrimSpace(layout) {
	case "minimal":
		renderMinimal(w, d, width)
	case "frame":
		if width < ResolveLimits(d.Limits).FrameWidth {
			renderSplit(w, d, width, height)
			return
		}
		renderFrame(w, d, width, height)
	default:
		renderSplit(w, d, width, height)
	}
}

func renderMinimal(w io.Writer, d Data, width int) {
	var buf bytes.Buffer
	_ = banner.Render(&buf, bannerInfo(d), ui.Current(), width, true)
	// banner's card has a floor width; clip so a tiny terminal still stays within W.
	// Lines that already fit are unchanged, so normal widths match banner.Render.
	lines := strings.Split(buf.String(), "\n")
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(clip(ln, width))
	}
	_, _ = io.WriteString(w, b.String())
}

func bannerInfo(d Data) banner.Info {
	model := ""
	for _, m := range d.Models {
		if m.Role == "main" && strings.TrimSpace(m.Name) != "" {
			model = m.Name
			break
		}
	}
	if model == "" {
		for _, m := range d.Models {
			if strings.TrimSpace(m.Name) != "" {
				model = m.Name
				break
			}
		}
	}
	branch := d.Session.Branch
	if branch != "not a git repo" {
		if labeled := branchLabel(d.Session); labeled != "" {
			branch = labeled
		}
	}
	return banner.Info{
		Version: d.Version,
		Model:   model,
		Dir:     d.Session.Dir,
		Branch:  branch,
		Host:    d.Host,
	}
}
