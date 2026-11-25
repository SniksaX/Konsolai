package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

type setRow struct {
	Header bool
	Key    config.Key
	Value  string
}

func menuRows(c config.Config) []setRow {
	first := []string{"ui.theme", "ui.appearance", "ui.welcome"}
	var rows []setRow
	rows = append(rows, setRow{Header: true, Value: "ui"})
	seen := map[string]bool{}
	for _, path := range first {
		k, ok := config.KeyByPath(path)
		if !ok {
			continue
		}
		seen[path] = true
		rows = append(rows, setRow{Key: k, Value: maskValue(k, c)})
	}
	section := "ui"
	for _, k := range config.Keys() {
		if seen[k.Path] {
			continue
		}
		sec := k.Path
		if i := strings.IndexByte(k.Path, '.'); i >= 0 {
			sec = k.Path[:i]
		}
		if sec != section {
			section = sec
			rows = append(rows, setRow{Header: true, Value: sec})
		}
		rows = append(rows, setRow{Key: k, Value: maskValue(k, c)})
	}
	return rows
}

func maskValue(k config.Key, c config.Config) string {
	if k.Secret {
		return config.MaskURL(k.Present(c))
	}
	return k.Present(c)
}

// setMenuLines renders every row once. The second result is the first line of
// the selected row, so the window can keep it on screen.
func setMenuLines(rows []setRow, sel int, detail bool) ([]string, int) {
	p := ui.Current()
	lines := make([]string, 0, len(rows))
	selLine := 0
	for i, row := range rows {
		if row.Header {
			lines = append(lines, p.Muted(row.Value))
			continue
		}
		name := row.Key.Path
		if row.Key.Name != "" && (row.Key.Path == "ui.theme" || row.Key.Path == "ui.appearance" || row.Key.Path == "ui.welcome") {
			name = row.Key.Name
		}
		line := fmt.Sprintf("  %-28s %s", name, row.Value)
		if i == sel {
			selLine = len(lines)
			line = p.Accent("› ") + fmt.Sprintf("%-28s %s", name, row.Value)
		}
		lines = append(lines, line)
		if detail && i == sel {
			lines = append(lines, "    "+p.Muted(row.Key.Doc))
		}
	}
	return lines, selLine
}

func joinSetMenuLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func formatSetMenu(rows []setRow, sel int, detail bool) string {
	lines, _ := setMenuLines(rows, sel, detail)
	return joinSetMenuLines(lines)
}

// setMenuHeight is the number of screen rows the picker may paint. One row is
// left for the prompt line above the menu.
func setMenuHeight() int {
	_, h := defaultTermSize()
	h--
	if h < 4 {
		h = 4
	}
	return h
}

// formatSetMenuWindow paints at most height rows, scrolled to keep the
// selected row visible. The "n more" markers match the slash menu.
func formatSetMenuWindow(rows []setRow, sel int, detail bool, height int) string {
	lines, selLine := setMenuLines(rows, sel, detail)
	if height <= 0 || len(lines) <= height {
		return joinSetMenuLines(lines)
	}
	if height < 4 {
		height = 4
	}
	content := height - 2 // one marker line above and below
	if content < 1 {
		content = 1
	}
	selEnd := selLine + 1
	if detail {
		selEnd++
	}
	top := selEnd - content
	if top < 0 {
		top = 0
	}
	if top > selLine {
		top = selLine
	}
	if maxTop := len(lines) - content; top > maxTop {
		top = maxTop
	}
	to := top + content
	if to > len(lines) {
		to = len(lines)
	}
	width := theme.TermWidth()
	var b strings.Builder
	if top > 0 {
		fmt.Fprintf(&b, "%s\n", ui.Current().Muted(fmt.Sprintf("↑ %d more", top)))
	}
	for _, line := range lines[top:to] {
		b.WriteString(theme.TruncateChrome(line, width))
		b.WriteByte('\n')
	}
	if to < len(lines) {
		fmt.Fprintf(&b, "%s\n", ui.Current().Muted(fmt.Sprintf("↓ %d more", len(lines)-to)))
	}
	return b.String()
}

func selectable(rows []setRow) []int {
	var idx []int
	for i, row := range rows {
		if !row.Header {
			idx = append(idx, i)
		}
	}
	return idx
}

func runSetMenu(ctx context.Context, env *Env, session bool) int {
	_ = ctx
	rows := menuRows(env.Profile)
	idx := selectable(rows)
	if len(idx) == 0 {
		fmt.Fprint(env.out(), formatSetMenu(rows, -1, false))
		return 0
	}
	pos := 0
	detail := false
	painted := 0
	erase := func() {
		if painted > 0 {
			fmt.Fprint(env.out(), theme.CursorUp(painted))
			fmt.Fprint(env.out(), theme.ClearBelow)
			painted = 0
		}
	}
	paint := func() {
		w := env.out()
		if painted > 0 {
			fmt.Fprint(w, theme.CursorUp(painted))
		}
		text := formatSetMenuWindow(rows, idx[pos], detail, setMenuHeight())
		painted = strings.Count(text, "\n")
		fmt.Fprint(w, text)
		fmt.Fprint(w, theme.ClearBelow)
	}
	paint()
	for {
		k, err := readSetKey()
		if err != nil {
			return 1
		}
		switch k {
		case "up":
			if pos > 0 {
				pos--
				paint()
			}
		case "down":
			if pos+1 < len(idx) {
				pos++
				paint()
			}
		case "?":
			detail = !detail
			paint()
		case "esc":
			return 0
		case "enter":
			row := rows[idx[pos]]
			if row.Key.Secret {
				fmt.Fprintf(env.errw(), "%s is a secret; set it with ~/.pgpass or the environment\n", row.Key.Path)
				continue
			}
			// The value editor and the apply line print below the menu, so
			// clear the menu first and repaint it at the same anchor after.
			erase()
			if code := editRow(env, row.Key, session); code != 0 {
				return code
			}
			rows = menuRows(env.Profile)
			paint()
		}
	}
}

func editRow(env *Env, k config.Key, session bool) int {
	if len(k.Allowed) > 0 || k.Path == "ui.theme" {
		return pickEnum(env, k, session)
	}
	if k.KindName() == "bool" {
		return pickEnum(env, k, session)
	}
	cur := k.Present(env.Profile)
	fmt.Fprintf(env.out(), "%s [%s]: ", k.Path, cur)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return 1
	}
	line = strings.TrimSpace(line)
	if line == "" || line == cur {
		return 0
	}
	if line == "default" {
		return commitKey(env, k, "", session, true)
	}
	return commitKey(env, k, line, session, false)
}

func pickEnum(env *Env, k config.Key, session bool) int {
	vals := k.Allowed
	if k.Path == "ui.theme" {
		vals = config.ThemeNames
	}
	if k.KindName() == "bool" && len(vals) == 0 {
		vals = []string{"on", "off"}
	}
	cur := k.Present(env.Profile)
	if k.KindName() == "bool" {
		if cur == "true" {
			cur = "on"
		} else {
			cur = "off"
		}
	}
	sel := 0
	for i, v := range vals {
		if strings.EqualFold(v, cur) {
			sel = i
			break
		}
	}
	painted := 0
	paint := func() {
		w := env.out()
		if painted > 0 {
			fmt.Fprint(w, theme.CursorUp(painted))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", ui.Current().Muted(k.Path))
		for i, v := range vals {
			if i == sel {
				fmt.Fprintf(&b, "%s\n", ui.Current().Accent("› "+v))
			} else {
				fmt.Fprintf(&b, "  %s\n", v)
			}
		}
		painted = strings.Count(b.String(), "\n")
		fmt.Fprint(w, b.String())
	}
	paint()
	for {
		key, err := readSetKey()
		if err != nil {
			return 1
		}
		switch key {
		case "up":
			if sel > 0 {
				sel--
				paint()
			}
		case "down":
			if sel+1 < len(vals) {
				sel++
				paint()
			}
		case "esc":
			return 0
		case "enter":
			raw := vals[sel]
			if k.KindName() == "bool" {
				if raw == "on" {
					raw = "true"
				} else {
					raw = "false"
				}
			}
			return commitKey(env, k, raw, session, false)
		}
	}
}

func readSetKey() (string, error) {
	b, err := ui.ReadKey()
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		return "enter", nil
	case '?':
		return "?", nil
	case 0x03:
		return "esc", nil
	case 0x1b:
		return readArrowOrEsc()
	default:
		return "", nil
	}
}
