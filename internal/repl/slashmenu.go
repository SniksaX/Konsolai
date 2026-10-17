package repl

import (
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/commands"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
)

const slashMenuRows = 8

type menuAction int

const (
	actionNone menuAction = iota
	actionLine
	actionSubmit
	actionClear
	actionPass
)

type menuResult struct {
	Action menuAction
	Line   string
}

type slashMenu struct {
	reg    *commands.Registry
	open   bool
	typed  string
	items  []commands.Command
	sel    int
	detail bool
	offset int
	hold   string
}

func newSlashMenu(reg *commands.Registry) *slashMenu {
	return &slashMenu{reg: reg, sel: -1}
}

func shouldOpenMenu(line string) bool {
	return strings.HasPrefix(line, "/") && !strings.Contains(line, " ")
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

func (m *slashMenu) Sync(line string) {
	if m == nil {
		return
	}
	if !shouldOpenMenu(line) {
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
		m.open = true
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
	if m == nil || !m.open || len(m.items) == 0 {
		return menuResult{Action: actionPass}
	}
	if m.sel == -1 {
		m.sel = 0
	} else {
		m.sel = (m.sel + 1) % len(m.items)
	}
	m.clampOffset()
	return menuResult{Action: actionLine, Line: "/" + m.items[m.sel].Name}
}

func (m *slashMenu) Up() menuResult {
	if m == nil || !m.open || len(m.items) == 0 {
		return menuResult{Action: actionPass}
	}
	if m.sel == -1 {
		m.sel = len(m.items) - 1
	} else if m.sel <= 0 {
		m.sel = -1
		m.detail = false
		m.clampOffset()
		return menuResult{Action: actionLine, Line: m.typed}
	} else {
		m.sel--
	}
	m.clampOffset()
	return menuResult{Action: actionLine, Line: "/" + m.items[m.sel].Name}
}

func (m *slashMenu) Question() menuResult {
	if m == nil || !m.open || m.sel < 0 {
		return menuResult{Action: actionPass}
	}
	m.detail = !m.detail
	return menuResult{Action: actionNone}
}

func (m *slashMenu) Tab() menuResult {
	if m == nil || !m.open || len(m.items) == 0 {
		return menuResult{Action: actionPass}
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

func (m *slashMenu) completeSelected() menuResult {
	line := "/" + m.items[m.sel].Name
	if m.items[m.sel].TakesArgs() {
		line += " "
	}
	m.typed = line
	if strings.Contains(line, " ") {
		m.hold = ""
		m.close()
	}
	return menuResult{Action: actionLine, Line: line}
}

func (m *slashMenu) Enter() menuResult {
	if m == nil || !m.open {
		return menuResult{Action: actionPass}
	}
	if m.sel >= 0 && m.items[m.sel].HasRequiredArgs() {
		line := "/" + m.items[m.sel].Name + " "
		m.typed = line
		m.hold = ""
		m.close()
		return menuResult{Action: actionLine, Line: line}
	}
	m.close()
	return menuResult{Action: actionSubmit}
}

func (m *slashMenu) Esc() menuResult {
	if m == nil {
		return menuResult{Action: actionPass}
	}
	if m.open {
		line := m.typed
		m.hold = line
		m.close()
		return menuResult{Action: actionLine, Line: line}
	}
	m.typed = ""
	m.hold = ""
	return menuResult{Action: actionClear}
}

func (m *slashMenu) clampOffset() {
	n := len(m.items)
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
	n := len(m.items)
	to = m.offset + slashMenuRows
	if to > n {
		to = n
	}
	return m.offset, to
}

func (m *slashMenu) Render(width int) string {
	if m == nil || !m.open || len(m.items) == 0 {
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
	for i := from; i < to; i++ {
		c := m.items[i]
		b.WriteString(theme.TruncateChrome(slashMenuRow(c, i == m.sel), width))
		b.WriteByte('\n')
	}
	if to < len(m.items) {
		b.WriteString(p.Muted(fmt.Sprintf("↓ %d more", len(m.items)-to)))
		b.WriteByte('\n')
	}
	if m.detail && m.sel >= 0 && m.sel < len(m.items) {
		b.WriteString(m.renderDetail())
	}
	return strings.TrimRight(b.String(), "\n")
}

func slashMenuRow(c commands.Command, selected bool) string {
	p := ui.Current()
	names := c.DisplayNames()
	if selected {
		return p.Accent("› ") + p.Heading(names) + "  " + p.Meta(c.Summary)
	}
	return "  " + theme.Style(theme.Primary, names) + "  " + p.Muted(c.Summary)
}

func (m *slashMenu) renderDetail() string {
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
		p.Accent("?") + " " + p.Muted("details") + " · " +
		p.Accent("tab") + " " + p.Muted("complete") + " · " +
		p.Accent("enter") + " " + p.Muted("run") + " · " +
		p.Accent("esc") + " " + p.Muted("cancel")
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
