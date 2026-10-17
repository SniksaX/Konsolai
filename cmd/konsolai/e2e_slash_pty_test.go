package main

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const (
	keyDown = "\x1b[B"
	keyUp   = "\x1b[A"
	keyEsc  = "\x1b"
	keyTab  = "\t"
)

func skipNoPTY(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
}

func startSlashE2E(t *testing.T) *e2eSess {
	t.Helper()
	srv := e2eLLM(t)
	s := startE2E(t, srv.URL+"/v1", t.TempDir())
	s.wait("›", 10*time.Second)
	return s
}

func currentSlashScreen(s *e2eSess) *slashScr {
	sc := newSlashScr(24, 80)
	s.mu.Lock()
	raw := append([]byte(nil), s.buf.Bytes()...)
	s.mu.Unlock()
	sc.feed(raw)
	return sc
}

func waitSlash(s *e2eSess, check func(*slashScr) bool, d time.Duration, what string) *slashScr {
	s.t.Helper()
	deadline := time.Now().Add(d)
	var last *slashScr
	for time.Now().Before(deadline) {
		last = currentSlashScreen(s)
		if check(last) {
			return last
		}
		time.Sleep(15 * time.Millisecond)
	}
	s.t.Fatalf("timeout waiting for %s\n%s\nplain:\n%s", what, last.dump(), s.plain())
	return last
}

func inputOf(sc *slashScr) string {
	for _, line := range sc.lines() {
		plain := strings.TrimRight(line, " ")
		if !strings.ContainsRune(plain, '›') {
			continue
		}
		if strings.Contains(plain, "select") || strings.Contains(plain, "details") {
			continue
		}
		i := strings.IndexRune(plain, '›')
		if i < 0 {
			continue
		}
		rest := plain[i+len("›"):]
		if strings.HasPrefix(rest, " ") {
			rest = rest[1:]
		}
		if strings.Contains(rest, "  ") {
			continue
		}
		return rest
	}
	return ""
}

func screenHas(sc *slashScr, needle string) bool {
	return strings.Contains(sc.dump(), needle)
}

func menuVisible(sc *slashScr) bool {
	n := 0
	for _, line := range sc.lines() {
		if strings.Contains(line, "/help") || strings.Contains(line, "Show this list") {
			n++
		}
	}
	return n > 0
}

func TestE2ESlash1Arrows(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/")
	waitSlash(s, func(sc *slashScr) bool { return screenHas(sc, "/help") }, 3*time.Second, "menu /help")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/help" }, 3*time.Second, "input /help")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/copy" }, 3*time.Second, "input /copy")
	s.send(keyUp)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/help" }, 3*time.Second, "back to /help")
	s.send(keyUp)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/" }, 3*time.Second, "restore /")
}

func TestE2ESlash2FilterTab(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/co")
	waitSlash(s, func(sc *slashScr) bool {
		d := sc.dump()
		return strings.Contains(d, "/copy") && strings.Contains(d, "/compact") && strings.Contains(d, "/config") && !strings.Contains(d, "/help")
	}, 3*time.Second, "/co matches")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool {
		in := inputOf(sc)
		return in == "/copy" || in == "/compact" || in == "/config"
	}, 3*time.Second, "first /co match")
	s.send(keyTab)
	waitSlash(s, func(sc *slashScr) bool {
		in := inputOf(sc)
		return in == "/copy " || in == "/compact" || in == "/compact " || in == "/config "
	}, 3*time.Second, "tab complete")
}

func TestE2ESlash3DetailToggle(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/")
	waitSlash(s, func(sc *slashScr) bool { return screenHas(sc, "/help") }, 3*time.Second, "menu")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/help" }, 3*time.Second, "/help")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/copy" }, 3*time.Second, "/copy")
	s.send("?")
	waitSlash(s, func(sc *slashScr) bool {
		d := sc.dump()
		return strings.Contains(d, "usage") && strings.Contains(d, "/copy")
	}, 3*time.Second, "detail on")
	s.send("?")
	waitSlash(s, func(sc *slashScr) bool { return !strings.Contains(sc.dump(), "usage") }, 3*time.Second, "detail off")
}

func TestE2ESlash4Esc(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/")
	waitSlash(s, func(sc *slashScr) bool { return screenHas(sc, "/help") }, 3*time.Second, "menu")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/help" }, 3*time.Second, "/help")
	s.send(keyEsc)
	time.Sleep(600 * time.Millisecond)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/" && !menuVisible(sc) }, 3*time.Second, "esc restore /")
	s.send(keyEsc)
	time.Sleep(600 * time.Millisecond)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "" }, 3*time.Second, "esc clear")
}

func TestE2ESlash5StatusEnter(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/sta")
	waitSlash(s, func(sc *slashScr) bool { return screenHas(sc, "/status") }, 3*time.Second, "/status listed")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/status" }, 3*time.Second, "select /status")
	s.send("\r")
	s.wait("version", 8*time.Second)
}

func TestE2ESlash6CdKeepsEditing(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/cd")
	waitSlash(s, func(sc *slashScr) bool { return screenHas(sc, "/cd") }, 3*time.Second, "/cd listed")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/cd" }, 3*time.Second, "select /cd")
	s.send("\r")
	waitSlash(s, func(sc *slashScr) bool {
		return inputOf(sc) == "/cd " && !strings.Contains(sc.dump(), "usage: /cd")
	}, 3*time.Second, "/cd stays")
}

func TestE2ESlash7History(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/status\r")
	s.wait("version", 8*time.Second)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "" && strings.Contains(sc.dump(), "›") }, 5*time.Second, "empty prompt")
	s.send(keyUp)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/status" }, 3*time.Second, "history /status")
}

func TestE2ESlash8CloseCursor(t *testing.T) {
	skipNoPTY(t)
	s := startSlashE2E(t)
	defer s.close()
	s.send("/")
	waitSlash(s, func(sc *slashScr) bool { return screenHas(sc, "/help") }, 3*time.Second, "menu")
	s.send(keyDown)
	waitSlash(s, func(sc *slashScr) bool { return inputOf(sc) == "/help" }, 3*time.Second, "/help")
	s.send(keyEsc)
	time.Sleep(600 * time.Millisecond)
	s.send(keyEsc)
	time.Sleep(600 * time.Millisecond)
	sc := waitSlash(s, func(sc *slashScr) bool {
		return inputOf(sc) == "" && !menuVisible(sc)
	}, 3*time.Second, "closed empty")
	row, col, ok := slashPromptCursor(sc)
	if !ok {
		t.Fatalf("prompt missing\n%s", sc.dump())
	}
	if sc.r != row {
		t.Fatalf("cursor row %d want %d\n%s", sc.r, row, sc.dump())
	}
	if sc.c < col-1 || sc.c > col+1 {
		t.Fatalf("cursor col %d want %d (after › )\n%s", sc.c, col, sc.dump())
	}
}

type slashScr struct {
	h, w int
	r, c int
	cell [][]rune
}

func newSlashScr(h, w int) *slashScr {
	s := &slashScr{h: h, w: w}
	s.cell = make([][]rune, h)
	for i := range s.cell {
		s.cell[i] = make([]rune, w)
		for j := range s.cell[i] {
			s.cell[i][j] = ' '
		}
	}
	return s
}

func (s *slashScr) lines() []string {
	out := make([]string, s.h)
	for i, row := range s.cell {
		out[i] = strings.TrimRight(string(row), " ")
	}
	return out
}

func (s *slashScr) dump() string { return strings.Join(s.lines(), "\n") }

func (s *slashScr) put(r rune) {
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

func (s *slashScr) scroll() {
	copy(s.cell, s.cell[1:])
	s.cell[s.h-1] = make([]rune, s.w)
	for j := range s.cell[s.h-1] {
		s.cell[s.h-1][j] = ' '
	}
}

func (s *slashScr) clearBelow() {
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

func (s *slashScr) clearLine() {
	if s.r < 0 || s.r >= s.h {
		return
	}
	for c := s.c; c < s.w; c++ {
		s.cell[s.r][c] = ' '
	}
}

func (s *slashScr) feed(p []byte) {
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

func (s *slashScr) csi(p []byte, i int) int {
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
		args := parseSlashCSIArgs(string(p[i+2 : j]))
		s.applyCSI(cmd, args)
		return j + 1
	default:
		return i + 2
	}
}

func parseSlashCSIArgs(s string) []int {
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

func (s *slashScr) applyCSI(cmd byte, args []int) {
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

func slashPromptCursor(sc *slashScr) (row, col int, ok bool) {
	for i, rowCells := range sc.cell {
		plain := strings.TrimRight(string(rowCells), " ")
		idx := strings.IndexRune(plain, '›')
		if idx < 0 {
			continue
		}
		if strings.Contains(plain, "select") {
			continue
		}
		return i, idx + 2, true
	}
	return 0, 0, false
}
