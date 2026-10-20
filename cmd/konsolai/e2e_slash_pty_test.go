package main

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSlashScreenSkipsDECSCUSR(t *testing.T) {
	sc := newSlashScreen(2, 40)
	sc.feed([]byte("ab\x1b[1 qcd"))
	if strings.Contains(sc.dump(), "[1 q") {
		t.Fatalf("cursor sequence leaked\n%s", sc.dump())
	}
	if !strings.Contains(sc.dump(), "abcd") {
		t.Fatalf("text\n%s", sc.dump())
	}
}

func TestE2ESlashMenuArrows(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("\x1b[B", slashPromptIs("› /help"))
	s.typeKeys("\x1b[B", slashPromptIs("› /copy"))
	s.typeKeys("\x1b[A", slashPromptIs("› /help"))
	s.typeKeys("\x1b[A", slashPromptIs("› /"))
	t.Log("PASS 1 arrows: / list, ↓ /help, ↓ /copy, ↑ /help, ↑ /")
}

func TestE2ESlashMenuFilterTab(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("co", func(sc *slashScreen) bool {
		d := sc.dump()
		return strings.Contains(d, "/copy") && !strings.Contains(d, "/help")
	})
	s.typeKeys("\x1b[B", slashPromptIs("› /copy"))
	s.typeKeys("\t", slashPromptIs("› /copy"))
	if p := s.promptLine(); !strings.HasPrefix(p, "› /copy") {
		s.failDump("tab complete prompt " + p)
	}
	t.Log("PASS 2 filter /co, ↓ first match, tab complete")
}

func TestE2ESlashMenuDetailToggle(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("\x1b[B", slashPromptIs("› /help"))
	s.typeKeys("\x1b[B", slashPromptIs("› /copy"))
	s.typeKeys("?", slashHasAll("usage", "example"))
	s.typeKeys("?", func(sc *slashScreen) bool { return !strings.Contains(sc.dump(), "usage") })
	t.Log("PASS 3 ? shows detail, ? hides it")
}

func TestE2ESlashMenuEscRestoreThenClear(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("\x1b[B", slashPromptIs("› /help"))
	s.typeKeys("\x1b", func(sc *slashScreen) bool {
		p := strings.TrimSpace(slashPromptText(sc))
		return p == "› /" && !strings.Contains(sc.dump(), "select")
	})
	s.typeKeys("\x1b", func(sc *slashScreen) bool {
		p := slashPromptText(sc)
		return p == "›" || p == "› "
	})
	t.Log("PASS 4 esc restores /, second esc clears")
}

func TestE2ESlashMenuEnterStatus(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("sta", slashHas("/status"))
	s.typeKeys("\x1b[B", slashPromptIs("› /status"))
	s.typeKeysWithin("\r", slashHas("version"), ptyTimeout()*3)
	t.Log("PASS 5 enter /status prints version")
}

func TestE2ESlashMenuEnterCDKeepsEditing(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("cd", func(sc *slashScreen) bool {
		p := strings.TrimSpace(slashPromptText(sc))
		return strings.Contains(sc.dump(), "parent directory") && (p == "› /cd" || p == "› /cd ")
	})
	s.typeKeys("\r", func(sc *slashScreen) bool {
		p := strings.TrimSpace(slashPromptText(sc))
		return strings.HasPrefix(p, "› /cd") && !strings.Contains(p, "..")
	})
	if strings.Contains(s.screen().dump(), "unknown") || strings.Contains(s.plain(), "usage:") {
		s.failDump("cd was submitted")
	}
	t.Log("PASS 6 enter /cd keeps editing")
}

func TestE2ESlashMenuClosedHistory(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeysWithin("/status\r", slashHas("version"), ptyTimeout()*3)
	s.expect("prompt again", func(sc *slashScreen) bool {
		p := slashPromptText(sc)
		return p == "›" || p == "› "
	})
	s.typeKeys("\x1b[A", slashPromptIs("› /status"))
	t.Log("PASS 7 closed ↑ recalls history")
}

func TestE2ESlashMenuCloseClearsRows(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeys("/", slashHas("/help"))
	s.typeKeys("\x1b", func(sc *slashScreen) bool {
		p := slashPromptText(sc)
		return strings.HasPrefix(p, "› /") && !strings.Contains(sc.dump(), "select")
	})
	s.typeKeys("\x1b", func(sc *slashScreen) bool {
		p := slashPromptText(sc)
		return (p == "›" || p == "› ") && slashMenuLeftover(sc) == ""
	})
	sc := s.screen()
	if leftover := slashMenuLeftover(sc); leftover != "" {
		s.failDump("leftover menu " + leftover)
	}
	row, col, ok := slashPromptCursor(sc)
	if !ok {
		s.failDump("prompt missing")
	}
	if sc.r != row {
		s.failDump("cursor row")
	}
	if sc.c < col-1 || sc.c > col+1 {
		s.failDump("cursor col")
	}
	t.Log("PASS 8 close clears menu, cursor after › ")
}

func startSlashE2E(t *testing.T) *e2eSess {
	t.Helper()
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	return startE2E(t, "http://127.0.0.1:9/v1", t.TempDir())
}

func (s *e2eSess) screen() *slashScreen {
	raw := s.raw()
	rows, cols := s.rows, s.cols
	if rows == 0 {
		rows = 24
	}
	if cols == 0 {
		cols = 80
	}
	sc := newSlashScreen(rows, cols)
	sc.feed(raw)
	return sc
}

func (s *e2eSess) plainScreen() string {
	return s.screen().dump()
}

// promptLine is the input row up to the cursor. Readline paints the inline
// suggestion after the cursor (dim gray fg 242); that text is not the buffer.
func (s *e2eSess) promptLine() string {
	sc := s.screen()
	for i, row := range sc.cell {
		plain := strings.TrimSpace(string(row))
		if !strings.HasPrefix(plain, "›") {
			continue
		}
		if sc.r == i && sc.c > 0 && sc.c <= len(row) {
			cut := strings.TrimSpace(string(row[:sc.c]))
			if strings.HasPrefix(cut, "›") {
				return cut
			}
		}
		return strings.TrimSpace(strings.TrimRight(string(row), " "))
	}
	return ""
}

func slashPromptText(sc *slashScreen) string {
	if sc != nil && sc.r >= 0 && sc.r < sc.h {
		row := sc.cell[sc.r]
		end := sc.c
		if end < 0 {
			end = 0
		}
		if end > len(row) {
			end = len(row)
		}
		cut := strings.TrimSpace(string(row[:end]))
		if strings.HasPrefix(cut, "›") {
			return cut
		}
		plain := strings.TrimSpace(strings.TrimRight(string(row), " "))
		if strings.HasPrefix(plain, "›") {
			return plain
		}
	}
	for _, row := range sc.cell {
		plain := strings.TrimSpace(string(row))
		if strings.HasPrefix(plain, "›") {
			return plain
		}
	}
	return ""
}

func slashPromptIs(want string) func(*slashScreen) bool {
	w := strings.TrimSpace(want)
	return func(sc *slashScreen) bool {
		p := strings.TrimSpace(slashPromptText(sc))
		if p == w {
			return true
		}
		// A completion may extend the line with a space and more text.
		if w != "›" && w != "› /" && strings.HasPrefix(p, w) {
			rest := strings.TrimPrefix(p, w)
			return rest == "" || strings.HasPrefix(rest, " ")
		}
		return false
	}
}

func (s *e2eSess) waitPromptLine(want string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	var last string
	w := strings.TrimSpace(want)
	for time.Now().Before(deadline) {
		last = strings.TrimSpace(s.promptLine())
		if last == w {
			return
		}
		if w != "›" && w != "› /" && strings.HasPrefix(last, w) {
			rest := strings.TrimPrefix(last, w)
			if rest == "" || strings.HasPrefix(rest, " ") {
				return
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	s.t.Fatalf("timeout prompt %q want %q\n--- screen ---\n%s\n--- raw ---\n%s", last, want, s.plainScreen(), e2eRawTail(s.raw()))
}

func (s *e2eSess) waitPromptMenu(needle string, d time.Duration) {
	s.t.Helper()
	s.waitScreen(needle, d)
	s.waitScreen("select", d)
}

func (s *e2eSess) waitScreen(needle string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(s.plainScreen(), needle) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	s.t.Fatalf("timeout screen %q\n--- screen ---\n%s\n--- raw ---\n%s", needle, s.plainScreen(), e2eRawTail(s.raw()))
}

func (s *e2eSess) waitScreenGone(needle string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.plainScreen(), needle) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	s.t.Fatalf("timeout waiting for %q to leave\n--- screen ---\n%s\n--- raw ---\n%s", needle, s.plainScreen(), e2eRawTail(s.raw()))
}

func slashPromptCursor(sc *slashScreen) (row, col int, ok bool) {
	for i, rowCells := range sc.cell {
		plain := strings.TrimRight(string(rowCells), " ")
		idx := strings.IndexRune(plain, '›')
		if idx < 0 || strings.Contains(plain, "select") {
			continue
		}
		return i, idx + 2, true
	}
	return 0, 0, false
}

func slashMenuLeftover(sc *slashScreen) string {
	for _, line := range sc.lines() {
		plain := strings.TrimSpace(ansiReE2E.ReplaceAllString(line, ""))
		if strings.Contains(plain, "/help") || strings.Contains(plain, "select") || strings.Contains(plain, " more") {
			return plain
		}
	}
	return ""
}

type slashScreen struct {
	h, w int
	r, c int
	cell [][]rune
}

func newSlashScreen(h, w int) *slashScreen {
	s := &slashScreen{h: h, w: w}
	s.cell = make([][]rune, h)
	for i := range s.cell {
		s.cell[i] = make([]rune, w)
		for j := range s.cell[i] {
			s.cell[i][j] = ' '
		}
	}
	return s
}

func (s *slashScreen) lines() []string {
	out := make([]string, s.h)
	for i, row := range s.cell {
		out[i] = strings.TrimRight(string(row), " ")
	}
	return out
}

func (s *slashScreen) dump() string { return strings.Join(s.lines(), "\n") }

func (s *slashScreen) put(r rune) {
	if r == '\n' {
		s.r++
		s.c = 0
		if s.r >= s.h {
			s.scroll()
			s.r = s.h - 1
		}
		return
	}
	if r == '\r' {
		s.c = 0
		return
	}
	if r == '\b' {
		if s.c > 0 {
			s.c--
		}
		return
	}
	if s.c >= s.w {
		s.r++
		s.c = 0
		if s.r >= s.h {
			s.scroll()
			s.r = s.h - 1
		}
	}
	if s.r >= 0 && s.r < s.h && s.c >= 0 && s.c < s.w {
		s.cell[s.r][s.c] = r
	}
	s.c++
}

func (s *slashScreen) scroll() {
	copy(s.cell, s.cell[1:])
	s.cell[s.h-1] = make([]rune, s.w)
	for j := range s.cell[s.h-1] {
		s.cell[s.h-1][j] = ' '
	}
}

func (s *slashScreen) clearBelow() {
	if s.r >= 0 && s.r < s.h {
		for c := s.c; c < s.w; c++ {
			s.cell[s.r][c] = ' '
		}
	}
	for r := s.r + 1; r < s.h; r++ {
		for c := 0; c < s.w; c++ {
			s.cell[r][c] = ' '
		}
	}
}

func (s *slashScreen) clearLine() {
	if s.r < 0 || s.r >= s.h {
		return
	}
	for c := s.c; c < s.w; c++ {
		s.cell[s.r][c] = ' '
	}
}

func (s *slashScreen) feed(p []byte) {
	i := 0
	for i < len(p) {
		if p[i] == 0x1b {
			i = s.csi(p, i)
			continue
		}
		r, n := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && n == 1 {
			i++
			continue
		}
		s.put(r)
		i += n
	}
}

func (s *slashScreen) csi(p []byte, i int) int {
	if i+1 >= len(p) {
		return i + 1
	}
	switch p[i+1] {
	case ']':
		j := i + 2
		for j < len(p) {
			if p[j] == 0x07 {
				return j + 1
			}
			if p[j] == 0x1b && j+1 < len(p) && p[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(p)
	case '[':
		j := i + 2
		for j < len(p) && p[j] >= 0x30 && p[j] <= 0x3F {
			j++
		}
		paramEnd := j
		for j < len(p) && p[j] >= 0x20 && p[j] <= 0x2F {
			j++
		}
		if j >= len(p) {
			return len(p)
		}
		if p[j] < 0x40 || p[j] > 0x7E {
			return j
		}
		s.applyCSI(p[j], parseSlashCSI(string(p[i+2:paramEnd])))
		return j + 1
	default:
		return i + 2
	}
}

func parseSlashCSI(s string) []int {
	if s == "" {
		return nil
	}
	var out []int
	cur := 0
	had := false
	for _, c := range s {
		if c >= '0' && c <= '9' {
			cur = cur*10 + int(c-'0')
			had = true
			continue
		}
		if c == ';' {
			if had {
				out = append(out, cur)
			} else {
				out = append(out, 0)
			}
			cur = 0
			had = false
		}
	}
	if had {
		out = append(out, cur)
	}
	return out
}

func (s *slashScreen) applyCSI(cmd byte, args []int) {
	n := 1
	if len(args) > 0 && args[0] > 0 {
		n = args[0]
	}
	switch cmd {
	case 'A':
		s.r -= n
		if s.r < 0 {
			s.r = 0
		}
	case 'B':
		s.r += n
		if s.r >= s.h {
			s.r = s.h - 1
		}
	case 'C':
		s.c += n
		if s.c >= s.w {
			s.c = s.w - 1
		}
	case 'D':
		s.c -= n
		if s.c < 0 {
			s.c = 0
		}
	case 'G':
		s.c = n - 1
		if s.c < 0 {
			s.c = 0
		}
	case 'H', 'f':
		row, col := 1, 1
		if len(args) > 0 && args[0] > 0 {
			row = args[0]
		}
		if len(args) > 1 && args[1] > 0 {
			col = args[1]
		}
		s.r, s.c = row-1, col-1
	case 'J':
		kind := 0
		if len(args) > 0 {
			kind = args[0]
		}
		if kind == 0 || kind == 2 {
			s.clearBelow()
		}
		if kind == 2 {
			s.r, s.c = 0, 0
			for r := 0; r < s.h; r++ {
				for c := 0; c < s.w; c++ {
					s.cell[r][c] = ' '
				}
			}
		}
	case 'K':
		s.clearLine()
	}
}
