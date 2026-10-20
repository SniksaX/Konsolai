package repl

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/SniksaX/Konsolai/internal/redact"
	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

const atScanCap = 2000

// atMenu is the file menu opened by @ at a word boundary.
type atMenu struct {
	cwd    func() string
	open   bool
	capped bool
	head   string
	tail   string
	typed  string
	hold   string
	rows   []atEntry
	sel    int
	offset int
	detail bool
}

type atEntry struct {
	rel       string
	abs       string
	dir       bool
	sensitive bool
	meta      string
}

func newAtMenu(cwd func() string) *atMenu {
	return &atMenu{cwd: cwd, sel: -1}
}

func (m *atMenu) Open() bool {
	return m != nil && m.open && (len(m.rows) > 0 || m.capped)
}

func (m *atMenu) SetRoot(cwd func() string) {
	if m == nil {
		return
	}
	m.cwd = cwd
}

func (m *atMenu) root() string {
	if m == nil || m.cwd == nil {
		return ""
	}
	return m.cwd()
}

func (m *atMenu) Sync(line string, cursor int) {
	if m == nil {
		return
	}
	head, query, tail, ok := atQuery(line, cursor)
	if !ok {
		if m.hold != "" && line == m.hold {
			m.open = false
			return
		}
		m.close()
		return
	}
	if m.hold != "" && line == m.hold {
		m.head, m.tail = head, tail
		return
	}
	m.hold = ""
	m.head, m.tail, m.typed = head, tail, query
	m.rows, m.capped = listAt(m.root(), query)
	m.sel = -1
	m.detail = false
	m.offset = 0
	m.open = len(m.rows) > 0 || m.capped
}

func (m *atMenu) close() {
	if m == nil {
		return
	}
	m.open = false
	m.capped = false
	m.rows = nil
	m.sel = -1
	m.detail = false
	m.offset = 0
	m.hold = ""
}

func (m *atMenu) Down() menuResult {
	if !m.Open() || len(m.rows) == 0 {
		return menuResult{Action: actionPass}
	}
	if m.sel == -1 {
		m.sel = 0
	} else {
		m.sel = (m.sel + 1) % len(m.rows)
	}
	m.clamp()
	return m.selected()
}

func (m *atMenu) Up() menuResult {
	if !m.Open() || len(m.rows) == 0 {
		return menuResult{Action: actionPass}
	}
	if m.sel == -1 {
		m.sel = len(m.rows) - 1
	} else if m.sel <= 0 {
		m.sel = -1
		m.detail = false
		m.clamp()
		line := m.head + "@" + m.typed + m.tail
		m.hold = line
		return menuResult{Action: actionLine, Line: line, Cursor: atCursor(m.head, m.typed), KeepCursor: true}
	} else {
		m.sel--
	}
	m.clamp()
	return m.selected()
}

func (m *atMenu) selected() menuResult {
	e := m.rows[m.sel]
	line := m.head + "@" + e.rel + m.tail
	m.hold = line
	return menuResult{Action: actionLine, Line: line, Cursor: atCursor(m.head, e.rel), KeepCursor: true}
}

func (m *atMenu) Enter() menuResult { return m.accept() }

func (m *atMenu) Tab() menuResult { return m.accept() }

func (m *atMenu) accept() menuResult {
	if !m.Open() || len(m.rows) == 0 {
		return menuResult{Action: actionPass}
	}
	idx := m.sel
	if idx < 0 {
		idx = 0
	}
	e := m.rows[idx]
	if e.dir {
		rel := e.rel
		if !strings.HasSuffix(rel, "/") {
			rel += "/"
		}
		m.typed = rel
		m.sel = -1
		m.detail = false
		m.hold = ""
		m.rows = nil
		line := m.head + "@" + rel + m.tail
		return menuResult{Action: actionLine, Line: line, Cursor: atCursor(m.head, rel), KeepCursor: true}
	}
	line := m.head + "@" + e.rel
	curQuery := e.rel
	if m.tail == "" || !strings.HasPrefix(m.tail, " ") {
		line += " "
		curQuery += " "
	}
	line += m.tail
	m.hold = line
	m.open = false
	m.sel = -1
	m.detail = false
	return menuResult{Action: actionLine, Line: line, Cursor: atCursor(m.head, curQuery), KeepCursor: true}
}

func (m *atMenu) Question() menuResult {
	if !m.Open() {
		return menuResult{Action: actionPass}
	}
	if m.sel < 0 {
		return menuResult{Action: actionNone}
	}
	m.detail = !m.detail
	return menuResult{Action: actionNone}
}

func (m *atMenu) Esc() menuResult {
	if m == nil || !m.open {
		return menuResult{Action: actionPass}
	}
	line := m.head + "@" + m.typed + m.tail
	m.hold = line
	m.open = false
	m.sel = -1
	m.detail = false
	return menuResult{Action: actionLine, Line: line, Cursor: atCursor(m.head, m.typed), KeepCursor: true}
}

func (m *atMenu) HintFooter() string {
	if !m.Open() {
		return ""
	}
	p := ui.Current()
	return p.Accent("↑↓") + " " + p.Muted("select") + " · " +
		p.Accent("enter") + " " + p.Muted("open/attach") + " · " +
		p.Accent("tab") + " " + p.Muted("open/attach") + " · " +
		p.Accent("?") + " " + p.Muted("details") + " · " +
		p.Accent("esc") + " " + p.Muted("back")
}

func (m *atMenu) Render(width int) string {
	if !m.Open() {
		return ""
	}
	if width < 16 {
		width = 16
	}
	p := ui.Current()
	if m.capped && len(m.rows) == 0 {
		return p.Muted("refine the path")
	}
	from, to := m.window()
	var b strings.Builder
	if from > 0 {
		b.WriteString(p.Muted(fmt.Sprintf("↑ %d more", from)))
		b.WriteByte('\n')
	}
	for i := from; i < to; i++ {
		b.WriteString(atRow(&m.rows[i], i == m.sel, width))
		b.WriteByte('\n')
	}
	if to < len(m.rows) {
		b.WriteString(p.Muted(fmt.Sprintf("↓ %d more", len(m.rows)-to)))
		b.WriteByte('\n')
	}
	if m.capped {
		b.WriteString(p.Muted("refine the path"))
		b.WriteByte('\n')
	}
	if m.detail && m.sel >= 0 && m.sel < len(m.rows) {
		if text := atDetail(&m.rows[m.sel]); text != "" {
			b.WriteString(p.Muted(text))
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *atMenu) clamp() {
	n := len(m.rows)
	if n <= slashMenuRows {
		m.offset = 0
		return
	}
	if m.sel < m.offset {
		m.offset = m.sel
	}
	if m.sel >= m.offset+slashMenuRows {
		m.offset = m.sel - slashMenuRows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > n-slashMenuRows {
		m.offset = n - slashMenuRows
	}
}

func (m *atMenu) window() (int, int) {
	n := len(m.rows)
	if n == 0 {
		return 0, 0
	}
	m.clamp()
	to := m.offset + slashMenuRows
	if to > n {
		to = n
	}
	return m.offset, to
}

func atCursor(head, query string) int {
	return len([]rune(head + "@" + query))
}

func atRow(e *atEntry, selected bool, width int) string {
	p := ui.Current()
	meta := e.right()
	metaW := theme.DisplayWidth(meta)
	mark := "  "
	markW := 2
	if selected {
		mark = "› "
	}
	room := width - markW - metaW - 1
	if room < 4 {
		room = 4
	}
	shown := e.rel
	if theme.DisplayWidth(shown) > room {
		shown = theme.Truncate(shown, room)
	}
	gap := width - markW - theme.DisplayWidth(shown) - metaW
	if gap < 1 {
		gap = 1
	}
	pad := strings.Repeat(" ", gap)
	if selected {
		return p.Accent("› ") + p.Heading(shown) + pad + p.Muted(meta)
	}
	return mark + theme.Style(theme.Primary, shown) + pad + p.Muted(meta)
}

func (e *atEntry) right() string {
	if e.meta != "" {
		return e.meta
	}
	if e.sensitive && !e.dir {
		e.meta = "sensitive"
		return e.meta
	}
	if e.dir {
		e.meta = fmt.Sprintf("dir · %d items", dirCount(e.abs))
		return e.meta
	}
	st, err := os.Stat(e.abs)
	if err != nil {
		e.meta = "binary"
		return e.meta
	}
	binary, lines := inspectFile(e.abs)
	if binary {
		e.meta = "binary"
		return e.meta
	}
	e.meta = fmt.Sprintf("%s · %d lines", formatSize(st.Size()), lines)
	return e.meta
}

func atDetail(e *atEntry) string {
	st, err := os.Stat(e.abs)
	var b strings.Builder
	b.WriteString(e.abs)
	if err == nil {
		fmt.Fprintf(&b, "\n%s\n%s", formatSize(st.Size()), st.ModTime().Format("2006-01-02 15:04"))
	}
	if e.dir {
		return b.String()
	}
	if e.sensitive {
		b.WriteString("\nsensitive")
		return b.String()
	}
	if lines, ok := firstTextLines(e.abs, 3); ok {
		for _, ln := range lines {
			b.WriteByte('\n')
			b.WriteString(ln)
		}
	}
	return b.String()
}

func listAt(cwd, query string) ([]atEntry, bool) {
	if cwd == "" {
		return nil, false
	}
	dirPart, base := query, ""
	if i := strings.LastIndex(query, "/"); i >= 0 {
		dirPart, base = query[:i], query[i+1:]
	} else {
		dirPart, base = "", query
	}
	absDir := cwd
	if dirPart != "" {
		absDir = filepath.Join(cwd, filepath.FromSlash(dirPart))
	}
	ents, err := os.ReadDir(absDir)
	if err != nil {
		return nil, false
	}
	if len(ents) > atScanCap {
		return nil, true
	}
	root := repo.Root(cwd)
	var paths []string
	for _, e := range ents {
		if e.Name() == ".git" && e.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(absDir, e.Name()))
	}
	ignored := gitIgnored(root, paths)
	var rows []atEntry
	for _, e := range ents {
		name := e.Name()
		if name == ".git" && e.IsDir() {
			continue
		}
		if base != "" && !strings.HasPrefix(name, base) {
			continue
		}
		abs := filepath.Join(absDir, name)
		rel := name
		if dirPart != "" {
			rel = dirPart + "/" + name
		}
		dir := e.IsDir()
		if dir {
			rel += "/"
		}
		sens := redact.SensitivePath(abs)
		if ignored[abs] && !sens {
			continue
		}
		rows = append(rows, atEntry{rel: rel, abs: abs, dir: dir, sensitive: sens})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].dir != rows[j].dir {
			return rows[i].dir
		}
		return rows[i].rel < rows[j].rel
	})
	return rows, false
}

func gitIgnored(root string, abs []string) map[string]bool {
	out := map[string]bool{}
	if root == "" || len(abs) == 0 {
		return out
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return out
	}
	var stdin bytes.Buffer
	back := map[string]string{}
	for _, p := range abs {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		back[rel] = p
		stdin.WriteString(rel)
		stdin.WriteByte(0)
	}
	if stdin.Len() == 0 {
		return out
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = &stdin
	raw, err := cmd.Output()
	if err != nil && len(raw) == 0 {
		return out
	}
	for _, rel := range strings.Split(string(raw), "\x00") {
		if rel == "" {
			continue
		}
		if p, ok := back[rel]; ok {
			out[p] = true
		}
	}
	return out
}

func dirCount(abs string) int {
	ents, err := os.ReadDir(abs)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if e.Name() == ".git" && e.IsDir() {
			continue
		}
		n++
	}
	return n
}

func inspectFile(path string) (binary bool, lines int) {
	f, err := os.Open(path)
	if err != nil {
		return true, 0
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	total := 0
	lines = 0
	saw := false
	endedNL := true
	for total < 1024*1024 {
		n, err := f.Read(buf)
		if n > 0 {
			saw = true
			chunk := buf[:n]
			if bytes.IndexByte(chunk, 0) >= 0 {
				return true, 0
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			endedNL = chunk[n-1] == '\n'
			total += n
		}
		if err != nil {
			break
		}
	}
	if !saw {
		return false, 0
	}
	if !endedNL {
		lines++
	}
	return false, lines
}

func firstTextLines(path string, n int) ([]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	buf := make([]byte, 8*1024)
	read, _ := f.Read(buf)
	buf = buf[:read]
	if bytes.IndexByte(buf, 0) >= 0 {
		return nil, false
	}
	text := string(buf)
	parts := strings.Split(text, "\n")
	var lines []string
	for _, ln := range parts {
		if len(lines) == n {
			break
		}
		if ln == "" && len(lines) == 0 {
			continue
		}
		lines = append(lines, ln)
	}
	return lines, true
}

func formatSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// atQuery finds an @ mention at a word boundary touching the cursor.
// query is the text between @ and the cursor. head and tail are the
// unchanged sides of the line.
func atQuery(line string, cursor int) (head, query, tail string, ok bool) {
	rs := []rune(line)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(rs) {
		cursor = len(rs)
	}
	tok := cursor
	for tok > 0 && !unicode.IsSpace(rs[tok-1]) {
		tok--
	}
	at := -1
	for i := cursor - 1; i >= tok; i-- {
		if rs[i] != '@' {
			continue
		}
		if i == 0 || !isWord(rs[i-1]) {
			at = i
			break
		}
	}
	if at < 0 {
		return "", "", "", false
	}
	return string(rs[:at]), string(rs[at+1 : cursor]), string(rs[cursor:]), true
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
