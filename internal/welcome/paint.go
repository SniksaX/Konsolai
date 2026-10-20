package welcome

import (
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/banner"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func wordmark(width int) []string {
	return wordmarkMin(width, 0)
}

func wordmarkMin(width, minW int) []string {
	if minW < 1 {
		minW = config.Builtin().LayoutTinyWidth()
	}
	if width < minW {
		return nil
	}
	var buf bytes.Buffer
	_ = banner.Render(&buf, banner.Info{MinWidth: minW}, ui.Current(), max(width, 80), true)
	var out []string
	for _, ln := range strings.Split(buf.String(), "\n") {
		if strings.ContainsAny(theme.StripANSI(ln), "█▀▄") {
			out = append(out, ln)
		}
	}
	return out
}

func taglineLine(pal ui.Palette) string {
	return spaces(markIndent) + pal.Muted("your terminal agent, on your own models")
}

func hintLine(pal ui.Palette, width int, tip string) string {
	key := func(k, word string) string {
		return pal.Accent(k) + " " + pal.Muted(word)
	}
	line := "    " + key("/", "commands") + "   " + key("!", "shell") + "   " + key("@", "files") + "   " + key("?", "help")
	tip = strings.TrimSpace(tip)
	if tip != "" {
		tipLine := pal.AccentDim("tip") + " " + pal.Muted(tip)
		gap := width - theme.DisplayWidth(line) - theme.DisplayWidth(tipLine)
		if gap >= 2 {
			return line + spaces(gap) + tipLine
		}
	}
	return clip(line, width)
}

func modelName(m Model) string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	if strings.TrimSpace(m.Role) != "" {
		return m.Role
	}
	return "model"
}

func modelRows(pal ui.Palette, inner int, models []Model) []string {
	if inner <= 0 {
		return nil
	}
	if len(models) == 0 {
		return []string{clip(" "+pal.Muted("no models"), inner)}
	}
	nameW := 0
	for _, m := range models {
		if w := theme.DisplayWidth(modelName(m)); w > nameW {
			nameW = w
		}
	}
	const roleW = 6
	// " " + dot + " " + name + "  " + role + "  " + stat. Keep a sliver for the stat.
	maxName := inner - (3 + 2 + roleW + 2 + 4)
	if maxName < 4 {
		maxName = max(1, inner/3)
	}
	if nameW > maxName {
		nameW = maxName
	}
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = paintModelRow(pal, inner, m, nameW, roleW)
	}
	return out
}

func paintModelRow(pal ui.Palette, inner int, m Model, nameW, roleW int) string {
	name := truncatePad(modelName(m), nameW)
	role := truncatePad(m.Role, roleW)
	stat := m.Stat
	if stat != "" {
		budget := inner - (3 + nameW + 2 + roleW + 2)
		if budget < 1 {
			budget = 1
		}
		stat = fitStat(stat, budget)
	}
	var b strings.Builder
	b.WriteString(" ")
	b.WriteString(paintDot(pal, m.Status))
	b.WriteString(" ")
	b.WriteString(pal.Meta(name))
	b.WriteString("  ")
	b.WriteString(pal.Muted(role))
	if stat != "" {
		b.WriteString("  ")
		b.WriteString(paintMetaErr(pal, stat))
	}
	return clip(b.String(), inner)
}

// fitStat keeps an error word intact when the full stat does not fit.
func fitStat(stat string, width int) string {
	if width <= 0 || stat == "" {
		return ""
	}
	if theme.DisplayWidth(stat) <= width {
		return stat
	}
	for _, w := range errWords {
		if strings.Contains(stat, w) && theme.DisplayWidth(w) <= width {
			return w
		}
	}
	return fitTail(stat, width)
}

func fitTail(s string, width int) string {
	if width <= 0 || s == "" {
		return ""
	}
	if theme.DisplayWidth(s) <= width {
		return s
	}
	rs := []rune(s)
	n := 0
	i := len(rs)
	for i > 0 {
		w := theme.DisplayWidth(string(rs[i-1]))
		if w < 1 {
			w = 1
		}
		if n+w > width {
			break
		}
		n += w
		i--
	}
	out := strings.TrimLeft(string(rs[i:]), " ")
	if out == "" || theme.DisplayWidth(out) > width {
		return string(rs[i:])
	}
	return out
}

func sessionLines(pal ui.Palette, inner int, s Session) []string {
	if inner <= 0 {
		return nil
	}
	var rows []string
	if s.Dir != "" {
		rows = append(rows, kvLine(pal, inner, "dir", s.Dir, true, pal.Meta))
	}
	if s.Branch == "not a git repo" {
		rows = append(rows, kvLine(pal, inner, "branch", s.Branch, false, pal.Muted))
	} else if b := branchLabel(s); b != "" {
		rows = append(rows, kvLine(pal, inner, "branch", b, false, pal.Accent))
	}
	if s.Changed > 0 {
		rows = append(rows, kvLine(pal, inner, "changed", strconv.Itoa(s.Changed), false, pal.Meta))
	}
	if s.Ahead > 0 {
		rows = append(rows, kvLine(pal, inner, "ahead", strconv.Itoa(s.Ahead), false, pal.Meta))
	}
	if s.Behind > 0 {
		rows = append(rows, kvLine(pal, inner, "behind", strconv.Itoa(s.Behind), false, pal.Meta))
	}
	if s.Build != "" {
		rows = append(rows, kvLine(pal, inner, "build", s.Build, true, func(v string) string {
			return paintBuild(pal, v)
		}))
	}
	if s.Memory != "" {
		rows = append(rows, kvLine(pal, inner, "memory", s.Memory, true, func(v string) string {
			return paintMetaErr(pal, v)
		}))
	}
	return rows
}

func kvLine(pal ui.Palette, inner int, key, val string, mid bool, paint func(string) string) string {
	if inner <= 0 {
		return ""
	}
	if paint == nil {
		paint = pal.Meta
	}
	const keyW = 7
	prefixW := 1 + keyW + 2
	room := inner - prefixW
	if room < 1 {
		room = 1
	}
	shown := val
	if theme.DisplayWidth(shown) > room {
		if mid {
			shown = theme.MiddleTruncate(shown, room)
		} else {
			shown = theme.Truncate(shown, room)
		}
	}
	line := " " + pal.Muted(padPlain(key, keyW)) + "  " + paint(shown)
	return clip(line, inner)
}

func paintBuild(pal ui.Palette, build string) string {
	ver, hash, ok := strings.Cut(build, "  ")
	if !ok || hash == "" {
		return paintMetaErr(pal, build)
	}
	return pal.Meta(ver) + "  " + pal.Muted(hash)
}

func recentLines(pal ui.Palette, width int, items []Recent, limit int) []string {
	if width <= 0 {
		return nil
	}
	if len(items) == 0 {
		return []string{clip(pal.Muted("no sessions yet"), width)}
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, recentText(pal, width, 0, item))
	}
	return out
}

func insetRecent(pal ui.Palette, width int, items []Recent) []string {
	if width <= 0 {
		return nil
	}
	if len(items) == 0 {
		return []string{clip(" "+pal.Muted("no sessions yet"), width)}
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = recentText(pal, width, 1, item)
	}
	return out
}

func recentText(pal ui.Palette, width, inset int, item Recent) string {
	if width <= 0 {
		return ""
	}
	if inset < 0 {
		inset = 0
	}
	if inset >= width {
		inset = width - 1
	}
	right := recentRight(item)
	rightW := theme.DisplayWidth(right)
	num := ""
	if item.N > 0 {
		num = strconv.Itoa(item.N) + " "
	}
	prefixW := inset + theme.DisplayWidth(num) + 2 // spaces, optional number, "›", and the following space
	minGap := 0
	if rightW > 0 {
		minGap = 1
	}
	room := width - prefixW - minGap - rightW
	if room < 0 {
		plain := spaces(inset) + num + "› " + right
		if theme.DisplayWidth(plain) > width {
			plain = theme.Truncate(plain, width)
		}
		return clip(paintRecentPlain(pal, plain), width)
	}
	title := item.Title
	if room == 0 {
		title = ""
	} else if theme.DisplayWidth(title) > room {
		title = theme.Truncate(title, room)
	}
	titleW := theme.DisplayWidth(title)
	gap := 0
	if rightW > 0 {
		gap = width - prefixW - titleW - rightW
		if gap < 1 {
			gap = 1
		}
	}
	var b strings.Builder
	b.WriteString(spaces(inset))
	b.WriteString(num)
	b.WriteString(pal.AccentDim("›"))
	b.WriteString(" ")
	b.WriteString(title)
	if rightW > 0 {
		b.WriteString(spaces(gap))
		b.WriteString(pal.Muted(right))
	}
	return clip(b.String(), width)
}

func recentRight(item Recent) string {
	var parts []string
	if item.Dir != "" {
		parts = append(parts, item.Dir)
	}
	if item.Intent != "" {
		parts = append(parts, item.Intent)
	}
	if item.Age != "" {
		parts = append(parts, item.Age)
	}
	return strings.Join(parts, "  ")
}

func paintRecentPlain(pal ui.Palette, plain string) string {
	lead := 0
	for lead < len(plain) && plain[lead] == ' ' {
		lead++
	}
	rest := plain[lead:]
	if strings.HasPrefix(rest, "›") {
		return spaces(lead) + pal.AccentDim("›") + pal.Muted(rest[len("›"):])
	}
	return pal.Muted(plain)
}

var errWords = []string{"unreachable", "offline", "timeout"}

func paintMetaErr(pal ui.Palette, s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		hit, word := -1, ""
		for _, w := range errWords {
			j := strings.Index(s[i:], w)
			if j >= 0 && (hit < 0 || j < hit) {
				hit, word = j, w
			}
		}
		if hit < 0 {
			b.WriteString(pal.Meta(s[i:]))
			break
		}
		if hit > 0 {
			b.WriteString(pal.Meta(s[i : i+hit]))
		}
		b.WriteString(pal.Error(word))
		i += hit + len(word)
	}
	return b.String()
}

func writeLines(w io.Writer, lines []string, width int) {
	for _, ln := range lines {
		_, _ = io.WriteString(w, clip(ln, width))
		_, _ = io.WriteString(w, "\n")
	}
}

func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if theme.DisplayWidth(s) <= width {
		return s
	}
	return theme.TruncateChrome(s, width)
}

func fitWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := theme.DisplayWidth(s)
	if w == width {
		return s
	}
	if w < width {
		return s + spaces(width-w)
	}
	return theme.TruncateChrome(s, width)
}

func padPlain(s string, width int) string {
	w := theme.DisplayWidth(s)
	if w >= width {
		return s
	}
	return s + spaces(width-w)
}

func truncatePad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if theme.DisplayWidth(s) > width {
		s = theme.Truncate(s, width)
	}
	return padPlain(s, width)
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}
