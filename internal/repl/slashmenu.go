package repl

import (
	"fmt"
	"strings"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

const slashMenuRows = 8

type menuAction int

const (
	actionNone menuAction = iota
	actionLine
	actionSubmit
	actionLineSubmit
	actionClear
	actionPass
)

type menuResult struct {
	Action     menuAction
	Line       string
	Cursor     int
	KeepCursor bool
}

type slashMenu struct {
	reg      *commands.Registry
	env      *commands.Env
	open     bool
	typed    string
	anchor   string
	seen     string
	items    []commands.Command
	rows     []commands.Choice
	raw      []commands.Choice
	arg      bool
	base     string
	partial  string
	cacheKey string
	sel      int
	detail   bool
	offset   int
	hold     string
	errText  string
	errLine  string
}

func newSlashMenu(reg *commands.Registry) *slashMenu {
	return &slashMenu{reg: reg, sel: -1}
}

func (m *slashMenu) Open() bool {
	return m != nil && m.open
}

func (m *slashMenu) Selected() int {
	if m == nil {
		return -1
	}
	return m.sel
}

func (m *slashMenu) Typed() string {
	if m == nil {
		return ""
	}
	return m.typed
}

func (m *slashMenu) Items() []commands.Command {
	if m == nil {
		return nil
	}
	return m.items
}

func (m *slashMenu) Detail() bool {
	return m != nil && m.detail
}

func (m *slashMenu) count() int {
	if m == nil {
		return 0
	}
	if m.arg {
		return len(m.rows)
	}
	return len(m.items)
}

func (m *slashMenu) Sync(line string) {
	if m == nil {
		return
	}
	if line != m.seen {
		m.seen = line
		if line != m.errLine {
			m.errText = ""
		}
	}
	if !strings.HasPrefix(line, "/") {
		m.close()
		m.typed = line
		m.hold = ""
		return
	}
	if m.hold != "" {
		if line == m.hold {
			m.open = false
			return
		}
		m.hold = ""
	}
	// Arrowing through command names must stay on that list. An exact
	// typed command such as /set still opens its argument level below.
	if !m.arg && m.sel >= 0 && m.sel < len(m.items) {
		want := "/" + m.items[m.sel].Name
		if line == want || line == m.typed {
			m.open = true
			return
		}
	}
	arg, base, partial, level, ok := m.argLevel(line)
	if arg {
		if !ok {
			m.close()
			m.typed = line
			return
		}
		if m.arg && m.sel >= 0 && m.sel < len(m.rows) {
			want := m.lineFor(m.rows[m.sel])
			if line == want || line == want+" " || line == m.anchor || line == m.typed {
				m.open = len(m.rows) > 0
				return
			}
		}
		m.arg = true
		m.items = nil
		m.base = base
		m.partial = partial
		m.rows = filterChoices(level, partial)
		m.anchor = line
		m.typed = line
		m.sel = -1
		m.detail = false
		m.clampOffset()
		m.open = len(m.rows) > 0
		return
	}
	if m.arg {
		m.arg = false
		m.rows = nil
		m.raw = nil
		m.cacheKey = ""
		m.sel = -1
	}
	if m.sel >= 0 && m.sel < len(m.items) {
		want := "/" + m.items[m.sel].Name
		if line == want || line == want+" " || line == m.typed {
			m.open = true
			return
		}
		m.typed = line
		m.sel = -1
		m.detail = false
		m.refilter()
		m.open = len(m.items) > 0
		return
	}
	if !m.open || line != m.typed {
		m.typed = line
		m.refilter()
	}
	m.open = len(m.items) > 0
}

func (m *slashMenu) close() {
	m.open = false
	m.sel = -1
	m.detail = false
	m.offset = 0
	m.items = nil
	m.rows = nil
	m.raw = nil
	m.arg = false
	m.cacheKey = ""
	m.partial = ""
	m.errText = ""
}

func (m *slashMenu) refilter() {
	if m.reg == nil {
		m.items = nil
		return
	}
	m.items = filterSlashCommands(m.reg, m.typed)
	if m.sel >= len(m.items) {
		m.sel = -1
		m.detail = false
	}
	m.clampOffset()
}

func (m *slashMenu) argLevel(line string) (arg bool, base, partial string, level []commands.Choice, ok bool) {
	if m.reg == nil || !strings.HasPrefix(line, "/") {
		return false, "", "", nil, false
	}
	body := strings.TrimPrefix(line, "/")
	if !strings.Contains(body, " ") {
		c := m.reg.Lookup(body)
		if c == nil || c.Tree == nil {
			return false, "", "", nil, false
		}
		if !strings.EqualFold(c.Name, body) && !aliasExact(c, body) {
			return false, "", "", nil, false
		}
		lv, good := m.level(c, nil)
		return true, "/" + c.Name + " ", "", lv, good
	}
	parts := strings.Fields(body)
	if len(parts) == 0 {
		return false, "", "", nil, false
	}
	c := m.reg.Lookup(parts[0])
	if c == nil || c.Tree == nil {
		return false, "", "", nil, false
	}
	trail := strings.HasSuffix(line, " ")
	args := parts[1:]
	var completed []string
	if trail {
		completed = args
		partial = ""
		base = line
	} else if len(args) == 0 {
		completed = nil
		partial = ""
		base = "/" + c.Name + " "
	} else {
		partial = args[len(args)-1]
		completed = args[:len(args)-1]
		if strings.HasSuffix(line, partial) {
			base = line[:len(line)-len(partial)]
		} else {
			base = "/" + c.Name + " "
		}
	}
	level, ok = m.level(c, completed)
	return true, base, partial, level, ok
}

func aliasExact(c *commands.Command, body string) bool {
	for _, a := range c.Aliases {
		if strings.EqualFold(a, body) {
			return true
		}
	}
	return false
}

func (m *slashMenu) level(c *commands.Command, completed []string) ([]commands.Choice, bool) {
	if c == nil || c.Tree == nil {
		return nil, false
	}
	key := c.Name + "\x00" + strings.Join(completed, "\x00")
	if key == m.cacheKey {
		return m.raw, true
	}
	cur := c.Tree(m.env)
	for _, arg := range completed {
		next, good := descendChoice(cur, arg)
		if !good {
			return nil, false
		}
		cur = next
	}
	m.cacheKey = key
	m.raw = cur
	return cur, true
}

func choiceHasNext(ch commands.Choice) bool {
	return ch.Dynamic != nil || len(ch.Next) > 0
}

func descendChoice(level []commands.Choice, arg string) ([]commands.Choice, bool) {
	for _, ch := range level {
		if ch.FreeText != nil {
			continue
		}
		if strings.EqualFold(ch.Value, arg) {
			if !choiceHasNext(ch) {
				return nil, false
			}
			return ch.Children(), true
		}
	}
	if len(level) == 1 && level[0].FreeText != nil && choiceHasNext(level[0]) {
		return level[0].Children(), true
	}
	return nil, false
}

func filterChoices(in []commands.Choice, partial string) []commands.Choice {
	if len(in) == 1 && in[0].FreeText != nil {
		return append([]commands.Choice(nil), in...)
	}
	if partial == "" {
		return append([]commands.Choice(nil), in...)
	}
	q := strings.ToLower(partial)
	var pre, sub, free []commands.Choice
	for _, ch := range in {
		if ch.FreeText != nil {
			free = append(free, ch)
			continue
		}
		v := strings.ToLower(ch.Value)
		if q == "" || strings.HasPrefix(v, q) {
			pre = append(pre, ch)
			continue
		}
		if strings.Contains(v, q) {
			sub = append(sub, ch)
		}
	}
	out := append(pre, sub...)
	if len(out) == 0 {
		return free
	}
	return out
}

func filterSlashCommands(reg *commands.Registry, typed string) []commands.Command {
	if reg == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimPrefix(typed, "/"))
	var prefix, sub []commands.Command
	for _, c := range reg.All() {
		pre, hasSub := false, false
		for _, n := range append([]string{c.Name}, c.Aliases...) {
			ln := strings.ToLower(n)
			if q == "" || strings.HasPrefix(ln, q) {
				pre = true
				break
			}
			if strings.Contains(ln, q) {
				hasSub = true
			}
		}
		if pre {
			prefix = append(prefix, c)
		} else if hasSub {
			sub = append(sub, c)
		}
	}
	return append(prefix, sub...)
}

func (m *slashMenu) Down() menuResult {
	if m == nil || !m.open || m.count() == 0 {
		return menuResult{Action: actionPass}
	}
	n := m.count()
	if m.sel == -1 {
		m.sel = 0
	} else {
		m.sel = (m.sel + 1) % n
	}
	m.clampOffset()
	return menuResult{Action: actionLine, Line: m.selectedLine()}
}

func (m *slashMenu) Up() menuResult {
	if m == nil || !m.open || m.count() == 0 {
		return menuResult{Action: actionPass}
	}
	if m.sel == -1 {
		m.sel = m.count() - 1
	} else if m.sel <= 0 {
		m.sel = -1
		m.detail = false
		m.clampOffset()
		line := m.typed
		if m.arg && m.anchor != "" {
			line = m.anchor
		}
		return menuResult{Action: actionLine, Line: line}
	} else {
		m.sel--
	}
	m.clampOffset()
	return menuResult{Action: actionLine, Line: m.selectedLine()}
}

func (m *slashMenu) selectedLine() string {
	if m.arg {
		if m.sel < 0 || m.sel >= len(m.rows) {
			return m.base + m.partial
		}
		return m.lineFor(m.rows[m.sel])
	}
	if m.sel < 0 || m.sel >= len(m.items) {
		return m.typed
	}
	return "/" + m.items[m.sel].Name
}

func (m *slashMenu) lineFor(ch commands.Choice) string {
	if ch.FreeText != nil {
		return m.base + m.partial
	}
	return m.base + ch.Value
}

func (m *slashMenu) Question() menuResult {
	if m == nil || !m.open || m.sel < 0 {
		return menuResult{Action: actionPass}
	}
	m.detail = !m.detail
	return menuResult{Action: actionNone}
}

func (m *slashMenu) Tab() menuResult {
	if m == nil || !m.open || m.count() == 0 {
		return menuResult{Action: actionPass}
	}
	if m.arg {
		return m.tabArg()
	}
	if m.sel >= 0 {
		return m.completeSelected()
	}
	names := make([]string, len(m.items))
	for i, c := range m.items {
		names[i] = "/" + c.Name
	}
	p := longestCommonPrefix(names)
	if p == "" || p == "/" {
		return menuResult{Action: actionNone}
	}
	m.typed = p
	m.refilter()
	return menuResult{Action: actionLine, Line: p}
}

func (m *slashMenu) tabArg() menuResult {
	if len(m.rows) == 1 && m.rows[0].FreeText != nil {
		return m.tabFree(m.rows[0])
	}
	if m.sel >= 0 && m.sel < len(m.rows) {
		ch := m.rows[m.sel]
		if ch.FreeText != nil {
			return m.tabFree(ch)
		}
		return m.completeChoice(ch)
	}
	vals := make([]string, 0, len(m.rows))
	for _, ch := range m.rows {
		if ch.FreeText == nil && ch.Value != "" {
			vals = append(vals, ch.Value)
		}
	}
	if len(vals) == 0 {
		return menuResult{Action: actionNone}
	}
	p := longestCommonPrefix(vals)
	if len(p) > len(m.partial) {
		line := m.base + p
		m.partial = p
		m.rows = filterChoices(m.raw, p)
		m.sel = -1
		m.typed = line
		m.anchor = line
		return menuResult{Action: actionLine, Line: line}
	}
	for _, ch := range m.rows {
		if ch.FreeText == nil && ch.Value != "" {
			return m.completeChoice(ch)
		}
	}
	return menuResult{Action: actionNone}
}

func (m *slashMenu) tabFree(ch commands.Choice) menuResult {
	if m.partial == "" {
		return menuResult{Action: actionNone}
	}
	if ch.FreeText != nil && ch.FreeText.Validate != nil {
		if err := ch.FreeText.Validate(m.partial); err != nil {
			m.errText = err.Error()
			m.errLine = m.base + m.partial
			return menuResult{Action: actionNone}
		}
	}
	line := m.base + m.partial
	if choiceHasNext(ch) {
		line += " "
	}
	m.sel = -1
	m.cacheKey = ""
	m.typed = line
	m.anchor = line
	return menuResult{Action: actionLine, Line: line}
}

func (m *slashMenu) completeChoice(ch commands.Choice) menuResult {
	line := m.base + ch.Value
	if choiceHasNext(ch) {
		line += " "
	}
	m.sel = -1
	m.cacheKey = ""
	m.typed = line
	m.anchor = line
	return menuResult{Action: actionLine, Line: line}
}

func (m *slashMenu) completeSelected() menuResult {
	c := m.items[m.sel]
	line := "/" + c.Name
	if c.Tree != nil {
		line += " "
		m.typed = line
		m.anchor = line
		m.sel = -1
		m.hold = ""
		m.cacheKey = ""
		return menuResult{Action: actionLine, Line: line}
	}
	if c.TakesArgs() {
		line += " "
		m.typed = line
		m.hold = ""
		m.close()
	}
	return menuResult{Action: actionLine, Line: line}
}

func (m *slashMenu) Enter() menuResult {
	if m == nil || !m.open {
		return menuResult{Action: actionPass}
	}
	if m.arg {
		return m.enterArg()
	}
	if m.sel >= 0 && m.sel < len(m.items) {
		c := m.items[m.sel]
		if c.HasRequiredArgs() {
			line := "/" + c.Name + " "
			m.typed = line
			m.anchor = line
			m.hold = ""
			if c.Tree != nil {
				m.sel = -1
				m.cacheKey = ""
				return menuResult{Action: actionLine, Line: line}
			}
			m.close()
			return menuResult{Action: actionLine, Line: line}
		}
	}
	m.close()
	return menuResult{Action: actionSubmit}
}

func (m *slashMenu) enterArg() menuResult {
	if m.sel < 0 {
		if len(m.rows) == 1 && m.rows[0].FreeText != nil {
			return m.enterFree(m.rows[0])
		}
		if ch, ok := m.exact(m.partial); ok {
			return m.enterChoice(ch)
		}
		if m.partial == "" && m.canSubmit(strings.TrimRight(m.base, " ")) {
			line := strings.TrimRight(m.base, " ")
			m.close()
			return menuResult{Action: actionLineSubmit, Line: line}
		}
		return menuResult{Action: actionNone}
	}
	if m.sel >= len(m.rows) {
		return menuResult{Action: actionNone}
	}
	ch := m.rows[m.sel]
	if ch.FreeText != nil {
		return m.enterFree(ch)
	}
	return m.enterChoice(ch)
}

func (m *slashMenu) canSubmit(line string) bool {
	if m == nil || m.reg == nil {
		return false
	}
	body := strings.TrimPrefix(strings.TrimSpace(line), "/")
	parts := strings.Fields(body)
	if len(parts) == 0 {
		return false
	}
	c := m.reg.Lookup(parts[0])
	if c == nil {
		return false
	}
	need := 0
	for _, a := range c.Args {
		if a.Required {
			need++
		}
	}
	return len(parts)-1 >= need
}

func (m *slashMenu) exact(partial string) (commands.Choice, bool) {
	if partial == "" {
		return commands.Choice{}, false
	}
	var hit commands.Choice
	n := 0
	for _, ch := range m.rows {
		if ch.FreeText != nil || ch.Value == "" {
			continue
		}
		if strings.EqualFold(ch.Value, partial) {
			hit = ch
			n++
		}
	}
	if n == 1 {
		return hit, true
	}
	return commands.Choice{}, false
}

func (m *slashMenu) enterChoice(ch commands.Choice) menuResult {
	line := m.base + ch.Value
	if ch.Terminal || !choiceHasNext(ch) {
		m.close()
		return menuResult{Action: actionLineSubmit, Line: line}
	}
	line += " "
	m.typed = line
	m.anchor = line
	m.sel = -1
	m.hold = ""
	m.cacheKey = ""
	return menuResult{Action: actionLine, Line: line}
}

func (m *slashMenu) enterFree(ch commands.Choice) menuResult {
	text := m.partial
	if strings.TrimSpace(text) == "" {
		m.errText = "enter a value"
		m.errLine = m.base + m.partial
		return menuResult{Action: actionNone}
	}
	if ch.FreeText != nil && ch.FreeText.Validate != nil {
		if err := ch.FreeText.Validate(text); err != nil {
			m.errText = err.Error()
			m.errLine = m.base + text
			return menuResult{Action: actionNone}
		}
	}
	line := m.base + text
	if ch.Terminal || !choiceHasNext(ch) {
		m.close()
		return menuResult{Action: actionLineSubmit, Line: line}
	}
	line += " "
	m.typed = line
	m.anchor = line
	m.sel = -1
	m.hold = ""
	m.cacheKey = ""
	return menuResult{Action: actionLine, Line: line}
}

func (m *slashMenu) Esc() menuResult {
	if m == nil {
		return menuResult{Action: actionPass}
	}
	if m.open {
		line := m.typed
		if m.arg {
			if m.partial == "" {
				line = strings.TrimRight(m.base, " ")
			} else if m.anchor != "" {
				line = m.anchor
			}
		}
		m.hold = line
		m.close()
		return menuResult{Action: actionLine, Line: line}
	}
	m.typed = ""
	m.hold = ""
	m.anchor = ""
	return menuResult{Action: actionClear}
}

func (m *slashMenu) clampOffset() {
	n := m.count()
	if n <= slashMenuRows {
		m.offset = 0
		return
	}
	if m.sel < 0 {
		if m.offset > n-slashMenuRows {
			m.offset = n - slashMenuRows
		}
		if m.offset < 0 {
			m.offset = 0
		}
		return
	}
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+slashMenuRows {
		m.offset = m.sel - slashMenuRows + 1
	}
}

func (m *slashMenu) window() (from, to int) {
	m.clampOffset()
	n := m.count()
	to = m.offset + slashMenuRows
	if to > n {
		to = n
	}
	return m.offset, to
}

func (m *slashMenu) Render(width int) string {
	if m == nil || !m.open || m.count() == 0 {
		return ""
	}
	if width < 16 {
		width = 16
	}
	p := ui.Current()
	from, to := m.window()
	var b strings.Builder
	if from > 0 {
		b.WriteString(p.Muted(fmt.Sprintf("↑ %d more", from)))
		b.WriteByte('\n')
	}
	// One name column for the whole filtered list, so summaries line up and do not shift while scrolling.
	nameW := 0
	if !m.arg {
		for _, c := range m.items {
			nameW = max(nameW, theme.DisplayWidth(c.DisplayNames()))
		}
	}
	for i := from; i < to; i++ {
		var row string
		if m.arg {
			row = choiceRow(m.rows[i], m.partial, i == m.sel, width)
		} else {
			row = slashMenuRow(m.items[i], i == m.sel, nameW)
		}
		b.WriteString(theme.TruncateChrome(row, width))
		b.WriteByte('\n')
	}
	if to < m.count() {
		b.WriteString(p.Muted(fmt.Sprintf("↓ %d more", m.count()-to)))
		b.WriteByte('\n')
	}
	if m.errText != "" {
		b.WriteString(p.Error(m.errText))
		b.WriteByte('\n')
	}
	if m.detail && m.sel >= 0 {
		if text := m.renderDetail(); text != "" {
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func slashMenuRow(c commands.Command, selected bool, nameW int) string {
	p := ui.Current()
	names := c.DisplayNames()
	gap := strings.Repeat(" ", max(0, nameW-theme.DisplayWidth(names))+2)
	if selected {
		return p.Accent("› ") + p.Heading(names) + gap + p.Meta(c.Summary)
	}
	return "  " + theme.Style(theme.Primary, names) + gap + p.Muted(c.Summary)
}

func choiceRow(ch commands.Choice, partial string, selected bool, width int) string {
	p := ui.Current()
	val := ch.Value
	placeholder := false
	if ch.FreeText != nil {
		if partial != "" {
			val = partial
		} else if ch.FreeText.Placeholder != "" {
			val = ch.FreeText.Placeholder
			placeholder = true
		}
	}
	desc := ch.Description
	cur := ""
	if ch.Current != nil {
		cur = ch.Current()
	}
	markW := 2
	curW := theme.DisplayWidth(cur)
	curGap := 0
	if cur != "" {
		curGap = 2
	}
	room := width - markW - curW - curGap
	if room < 4 {
		room = 4
	}
	if theme.DisplayWidth(val) > room-2 {
		val = theme.Truncate(val, max(1, room-2))
	}
	valW := theme.DisplayWidth(val)
	descRoom := room - valW - 2
	if descRoom < 0 {
		descRoom = 0
	}
	if theme.DisplayWidth(desc) > descRoom {
		desc = theme.Truncate(desc, descRoom)
	}
	used := markW + valW + 2 + theme.DisplayWidth(desc)
	pad := width - used - curGap - curW
	if pad < 1 {
		pad = 1
	}
	var b strings.Builder
	if selected {
		b.WriteString(p.Accent("› "))
	} else {
		b.WriteString("  ")
	}
	switch {
	case placeholder:
		b.WriteString(p.Muted(val))
	case selected:
		b.WriteString(p.Heading(val))
	default:
		b.WriteString(theme.Style(theme.Primary, val))
	}
	b.WriteString("  ")
	if selected && !placeholder {
		b.WriteString(p.Meta(desc))
	} else {
		b.WriteString(p.Muted(desc))
	}
	b.WriteString(strings.Repeat(" ", pad))
	if cur != "" {
		b.WriteString("  ")
		b.WriteString(p.Meta(cur))
	}
	return b.String()
}

func (m *slashMenu) renderDetail() string {
	if m.arg {
		if m.sel < 0 || m.sel >= len(m.rows) {
			return ""
		}
		ch := m.rows[m.sel]
		if ch.Detail != nil {
			return ch.Detail()
		}
		return ""
	}
	if m.sel < 0 || m.sel >= len(m.items) {
		return ""
	}
	c := m.items[m.sel]
	p := ui.Current()
	var b strings.Builder
	usage := c.Usage
	if usage != "" && !strings.HasPrefix(usage, "/") {
		usage = "/" + usage
	}
	if usage != "" {
		b.WriteString(p.Heading("usage") + "    " + p.Muted(usage) + "\n")
	}
	if len(c.Args) > 0 {
		parts := make([]string, 0, len(c.Args))
		for _, a := range c.Args {
			if a.Help != "" {
				parts = append(parts, a.Name+": "+a.Help)
			} else {
				parts = append(parts, a.Name)
			}
		}
		b.WriteString(p.Heading("args") + "     " + p.Muted(strings.Join(parts, "; ")) + "\n")
	}
	if c.Example != "" {
		b.WriteString(p.Heading("example") + "  " + p.Muted(c.Example))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *slashMenu) HintFooter() string {
	if m == nil || !m.open {
		return ""
	}
	p := ui.Current()
	return p.Accent("↑↓") + " " + p.Muted("select") + " · " +
		p.Accent("enter") + " " + p.Muted("next/run") + " · " +
		p.Accent("tab") + " " + p.Muted("complete") + " · " +
		p.Accent("?") + " " + p.Muted("details") + " · " +
		p.Accent("esc") + " " + p.Muted("back")
}

func longestCommonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	p := names[0]
	for _, s := range names[1:] {
		for !strings.HasPrefix(s, p) {
			if p == "" {
				return ""
			}
			p = p[:len(p)-1]
		}
	}
	return p
}
