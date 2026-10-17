package welcome

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/siraj/konsolai/internal/banner"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
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
		if width < d.Limits.norm().FrameWidth {
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

func wordmark(width int) []string {
	return wordmarkMin(width, 0)
}

func wordmarkMin(width, minW int) []string {
	if minW < 1 {
		minW = 64
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

type healthKind int

const (
	healthOnline healthKind = iota
	healthDegraded
	healthOffline
)

func healthOf(d Data) healthKind {
	if len(d.Models) == 0 {
		if d.Total > 0 && d.Up >= d.Total {
			return healthOnline
		}
		if d.Up == 0 {
			return healthOffline
		}
		return healthDegraded
	}
	up, bad := 0, 0
	for _, m := range d.Models {
		switch m.Status {
		case StatusUp:
			up++
		case StatusDown, StatusTimeout:
			bad++
		}
	}
	n := len(d.Models)
	if up == n {
		return healthOnline
	}
	if bad == n {
		return healthOffline
	}
	return healthDegraded
}

func hostName(d Data) string {
	if h := strings.TrimSpace(d.Host); h != "" {
		return h
	}
	return "fatboy"
}

func statusStrip(pal ui.Palette, d Data, width int) string {
	var segs []string
	switch healthOf(d) {
	case healthOffline:
		segs = append(segs, pal.Error("○")+" "+pal.Meta(hostName(d))+" "+pal.Error("offline"))
	default:
		dot := pal.Success("●")
		if healthOf(d) == healthDegraded && d.Up == 0 {
			dot = pal.Warn("●")
		}
		segs = append(segs, dot+" "+pal.Meta(fmt.Sprintf("%d/%d", d.Up, d.Total))+" "+pal.Muted("up"))
		if healthOf(d) == healthDegraded {
			for _, m := range d.Models {
				if m.Status == StatusUp {
					continue
				}
				name := modelName(m)
				segs = append(segs, paintDot(pal, m.Status)+" "+pal.Meta(name))
			}
		}
	}
	switch {
	case d.Session.Branch == "not a git repo":
		segs = append(segs, pal.Muted("not a git repo"))
	case branchLabel(d.Session) != "":
		seg := pal.Accent(branchLabel(d.Session))
		if d.Session.Changed > 0 {
			n := strconv.Itoa(d.Session.Changed)
			seg += " " + pal.Meta(n) + " " + pal.Muted("changed")
		}
		segs = append(segs, seg)
	case d.Session.Changed > 0:
		n := strconv.Itoa(d.Session.Changed)
		segs = append(segs, pal.Meta(n)+" "+pal.Muted("changed"))
	}
	if d.Session.Ahead > 0 {
		n := strconv.Itoa(d.Session.Ahead)
		segs = append(segs, pal.Muted("ahead")+" "+pal.Meta(n))
	}
	if d.Session.Behind > 0 {
		n := strconv.Itoa(d.Session.Behind)
		segs = append(segs, pal.Muted("behind")+" "+pal.Meta(n))
	}
	if mem := strings.TrimSpace(d.Session.Memory); mem != "" {
		segs = append(segs, paintMetaErr(pal, mem))
	}
	var b strings.Builder
	for i, seg := range segs {
		if i > 0 {
			b.WriteString(" ")
			b.WriteString(pal.Subtle("·"))
			b.WriteString(" ")
		}
		b.WriteString(seg)
	}
	return clip("  "+b.String(), width)
}

func branchLabel(s Session) string {
	if s.Branch == "" || s.Branch == "not a git repo" {
		return ""
	}
	b := s.Branch
	if s.Dirty && !strings.HasSuffix(b, "*") {
		b += "*"
	}
	return b
}

func paintDot(pal ui.Palette, st Status) string {
	switch st {
	case StatusUp:
		return pal.Success("●")
	case StatusSlow:
		return pal.Warn("●")
	case StatusDown, StatusTimeout:
		return pal.Error("○")
	default:
		return pal.Muted("○")
	}
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
	prefixW := inset + 2 // spaces, "›", and the following space
	minGap := 0
	if rightW > 0 {
		minGap = 1
	}
	room := width - prefixW - minGap - rightW
	if room < 0 {
		plain := spaces(inset) + "› " + right
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
	switch {
	case item.Intent != "" && item.Age != "":
		return item.Intent + "  " + item.Age
	case item.Intent != "":
		return item.Intent
	default:
		return item.Age
	}
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
