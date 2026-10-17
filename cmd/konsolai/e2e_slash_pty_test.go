package main

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestE2ESlashMenuArrows(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/")
	s.waitPromptMenu("/help", 3*time.Second)
	s.send("\x1b[B")
	s.waitPromptLine("› /help", 3*time.Second)
	s.send("\x1b[B")
	s.waitPromptLine("› /copy", 3*time.Second)
	s.send("\x1b[A")
	s.waitPromptLine("› /help", 3*time.Second)
	s.send("\x1b[A")
	s.waitPromptLine("› /", 3*time.Second)
	t.Log("PASS 1 arrows: / list, ↓ /help, ↓ /copy, ↑ /help, ↑ /")
}

func TestE2ESlashMenuFilterTab(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/co")
	s.waitPromptMenu("/copy", 3*time.Second)
	if sc := s.plainScreen(); strings.Contains(sc, "/help") && !strings.Contains(sc, "/copy") {
		t.Fatalf("filter leaked /help:\n%s", sc)
	}
	s.send("\x1b[B")
	s.waitPromptLine("› /copy", 3*time.Second)
	s.send("\t")
	s.waitPromptLine("› /copy", 3*time.Second)
	if !strings.Contains(s.promptLine(), "› /copy") {
		t.Fatalf("tab complete:\n%s", s.plainScreen())
	}
	t.Log("PASS 2 filter /co, ↓ first match, tab complete")
}

func TestE2ESlashMenuDetailToggle(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/")
	s.waitPromptMenu("/help", 3*time.Second)
	s.send("\x1b[B")
	s.send("\x1b[B")
	s.waitPromptLine("› /copy", 3*time.Second)
	s.send("?")
	s.waitScreen("usage", 3*time.Second)
	if !strings.Contains(s.plainScreen(), "example") {
		t.Fatalf("detail missing example:\n%s", s.plainScreen())
	}
	s.send("?")
	s.waitScreenGone("usage", 3*time.Second)
	t.Log("PASS 3 ? shows detail, ? hides it")
}

func TestE2ESlashMenuEscRestoreThenClear(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/")
	s.waitPromptMenu("/help", 3*time.Second)
	s.send("\x1b[B")
	s.waitPromptLine("› /help", 3*time.Second)
	s.send("\x1b")
	time.Sleep(600 * time.Millisecond)
	s.waitPromptLine("› /", 3*time.Second)
	if strings.Contains(s.plainScreen(), "select") {
		t.Fatalf("menu still open after esc:\n%s", s.plainScreen())
	}
	s.send("\x1b")
	time.Sleep(600 * time.Millisecond)
	s.waitPromptLine("›", 3*time.Second)
	if p := s.promptLine(); p != "›" && p != "› " {
		t.Fatalf("second esc should clear, prompt %q\n%s", p, s.plainScreen())
	}
	t.Log("PASS 4 esc restores /, second esc clears")
}

func TestE2ESlashMenuEnterStatus(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/sta")
	s.waitPromptMenu("/status", 3*time.Second)
	s.send("\x1b[B")
	s.waitPromptLine("› /status", 3*time.Second)
	mark := s.len()
	s.send("\r")
	s.waitNew(mark, "version", 8*time.Second)
	t.Log("PASS 5 enter /status prints version")
}

func TestE2ESlashMenuEnterCDKeepsEditing(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/cd")
	s.waitPromptMenu("/cd", 3*time.Second)
	s.send("\x1b[B")
	s.waitPromptLine("› /cd", 3*time.Second)
	s.send("\r")
	s.waitPromptLine("› /cd", 3*time.Second)
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(s.plain(), "unknown") || strings.Contains(s.plainScreen(), "version") {
		t.Fatalf("cd was submitted:\n%s", s.plain())
	}
	if !strings.Contains(s.promptLine(), "› /cd") {
		t.Fatalf("want /cd still editing, prompt %q\n%s", s.promptLine(), s.plainScreen())
	}
	t.Log("PASS 6 enter /cd keeps editing")
}

func TestE2ESlashMenuClosedHistory(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/status\r")
	s.wait("version", 8*time.Second)
	s.wait("›", 8*time.Second)
	time.Sleep(150 * time.Millisecond)
	s.send("\x1b[A")
	s.waitPromptLine("› /status", 3*time.Second)
	t.Log("PASS 7 closed ↑ recalls history")
}

func TestE2ESlashMenuCloseClearsRows(t *testing.T) {
	s := startSlashE2E(t)
	defer s.close()
	s.wait("›", 10*time.Second)
	s.send("/")
	s.waitPromptMenu("/help", 3*time.Second)
	s.send("\x1b")
	time.Sleep(600 * time.Millisecond)
	s.send("\x1b")
	time.Sleep(600 * time.Millisecond)
	sc := s.screen()
	if leftover := slashMenuLeftover(sc); leftover != "" {
		t.Fatalf("leftover menu %q:\n%s", leftover, sc.dump())
	}
	row, col, ok := slashPromptCursor(sc)
	if !ok {
		t.Fatalf("prompt missing:\n%s", sc.dump())
	}
	if sc.r != row {
		t.Fatalf("cursor row %d want %d\n%s", sc.r, row, sc.dump())
	}
	if sc.c < col-1 || sc.c > col+1 {
		t.Fatalf("cursor col %d want %d (after › )\n%s", sc.c, col, sc.dump())
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
	s.mu.Lock()
	raw := append([]byte(nil), s.buf.Bytes()...)
	s.mu.Unlock()
	sc := newSlashScreen(24, 80)
	sc.feed(raw)
	return sc
}

func (s *e2eSess) plainScreen() string {
	return s.screen().dump()
}

func (s *e2eSess) promptLine() string {
	for _, line := range s.screen().lines() {
		p := strings.TrimSpace(ansiReE2E.ReplaceAllString(line, ""))
		if strings.HasPrefix(p, "›") {
			return p
		}
	}
	return ""
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
	s.t.Fatalf("timeout prompt %q want %q\n%s", last, want, s.plainScreen())
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
	s.t.Fatalf("timeout screen %q\n%s", needle, s.plainScreen())
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
	s.t.Fatalf("timeout waiting for %q to leave\n%s", needle, s.plainScreen())
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
		for j < len(p) && !((p[j] >= 'A' && p[j] <= 'Z') || (p[j] >= 'a' && p[j] <= 'z')) {
			j++
		}
		if j >= len(p) {
			return len(p)
		}
		cmd := p[j]
		args := parseSlashCSI(string(p[i+2 : j]))
		s.applyCSI(cmd, args)
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
