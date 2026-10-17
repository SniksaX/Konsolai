package banner

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
)

func HostOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return strings.TrimSpace(endpoint)
	}
	return u.Hostname()
}

func ParseBuild(ver string) string {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		return ""
	}
	dirty := strings.HasSuffix(ver, "-dirty")
	ver = strings.TrimSuffix(ver, "-dirty")
	if i := strings.Index(ver, "-g"); i > 0 {
		tag := ver[:i]
		if j := strings.LastIndex(tag, "-"); j > 0 {
			if _, err := strconv.Atoi(tag[j+1:]); err == nil {
				tag = tag[:j]
			}
		}
		hash := ver[i+2:]
		out := tag + "  " + hash
		if dirty {
			out += "*"
		}
		return out
	}
	return ver
}

func Compact(info Info, pal ui.Palette) string {
	ver := ParseBuild(info.Version)
	if ver == "" {
		ver = info.Version
	}
	model := info.Model
	if model == "" {
		model = "-"
	}
	right := ver
	if model != "-" {
		right += " · " + model
	}
	line := indent + pal.Accent("›") + " " + pal.Heading("KONSOLAI") + "  " + pal.Muted(right)
	return theme.TruncateChrome(line, theme.TermWidth())
}

func card(info Info, pal ui.Palette, width int) string {
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	bar := strings.Repeat("─", inner)
	top := pal.Subtle("╭" + bar + "╮")
	bot := pal.Subtle("╰" + bar + "╯")
	modelVal := info.Model
	if modelVal == "" {
		modelVal = "-"
	}
	if info.Host != "" && modelVal != "-" {
		modelVal += " on " + info.Host
	}
	dirVal := info.Dir
	if info.Branch != "" {
		dirVal = strings.TrimSpace(dirVal)
	}
	buildVal := ParseBuild(info.Version)
	if buildVal == "" {
		buildVal = info.Version
	}
	var b strings.Builder
	b.WriteString(indent + top + "\n")
	b.WriteString(cardRow(pal, inner, "model", modelVal, ""))
	b.WriteString(cardRow(pal, inner, "dir", dirVal, info.Branch))
	b.WriteString(cardRow(pal, inner, "build", buildVal, ""))
	b.WriteString(indent + bot + "\n")
	return b.String()
}

func cardRow(pal ui.Palette, inner int, key, value, branch string) string {
	label := pal.Muted(padRight(key, 6))
	val := value
	room := inner - 2 - 6 - 2
	if branch != "" {
		br := "  " + branch
		room -= theme.DisplayWidth(br)
		val = theme.MiddleTruncate(val, max(4, room))
		line := "  " + label + "  " + pal.Meta(val) + "  " + pal.Accent(branch)
		return indent + pal.Subtle("│") + padInner(line, inner) + pal.Subtle("│") + "\n"
	}
	val = theme.MiddleTruncate(val, max(4, room))
	line := "  " + label + "  " + pal.Meta(val)
	return indent + pal.Subtle("│") + padInner(line, inner) + pal.Subtle("│") + "\n"
}

func padRight(s string, n int) string {
	for theme.DisplayWidth(s) < n {
		s += " "
	}
	return s
}

func padInner(plain string, inner int) string {
	// plain includes ANSI; pad based on display width of stripped... use DisplayWidth which counts ESC as 0?
	// DisplayWidth uses graphemes and will count ESC poorly. Use vis-like skip.
	w := visWidth(plain)
	if w >= inner {
		return plain
	}
	return plain + strings.Repeat(" ", inner-w)
}

func visWidth(s string) int {
	n := 0
	skip := false
	for _, r := range s {
		if r == 0x1b {
			skip = true
			continue
		}
		if skip {
			if r == 'm' {
				skip = false
			}
			continue
		}
		n += theme.DisplayWidth(string(r))
	}
	return n
}

func hintLine(pal ui.Palette, width int) string {
	key := func(k, w string) string {
		return pal.Accent(k) + " " + pal.Muted(w)
	}
	line := indent + "  " + key("/", "commands") + "   " + key("!", "shell") + "   " + key("@", "files") + "   " + key("?", "help")
	return theme.TruncateChrome(line, width)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
