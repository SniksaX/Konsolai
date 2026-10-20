package repl

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/creack/pty"
)

// CSI with parameters, intermediates, and one final byte. DECSCUSR is
// ESC [ n SP q, which a letter-only final class leaves in the text.
var ansiRe = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`)

func ptyScale() int {
	s := os.Getenv("KONSOLAI_TEST_TIMEOUT_SCALE")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func ptyTimeout() time.Duration {
	return 3 * time.Second * time.Duration(ptyScale())
}

func rawTail(b []byte) string {
	if len(b) > 2048 {
		b = b[len(b)-2048:]
	}
	return strconv.Quote(string(b))
}

func failScreen(t *testing.T, why string, sc *screen, raw []byte) {
	t.Helper()
	dump := ""
	if sc != nil {
		dump = sc.dump()
	}
	t.Fatalf("%s\n--- screen ---\n%s\n--- raw ---\n%s", why, dump, rawTail(raw))
}

// ptyView is a PTY whose expect helper reads the rendered screen.
type ptyView struct {
	t    *testing.T
	pt   *os.File
	mu   *sync.Mutex
	buf  *bytes.Buffer
	mark int
	rows int
	cols int
}

func (v *ptyView) raw() []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]byte(nil), v.buf.Bytes()...)
}

func (v *ptyView) shot() *screen {
	sc := newScreen(v.rows, v.cols)
	sc.feed(v.raw())
	return sc
}

func (v *ptyView) expect(why string, ok func(*screen) bool) *screen {
	v.t.Helper()
	deadline := time.Now().Add(ptyTimeout())
	var last *screen
	var raw []byte
	for time.Now().Before(deadline) {
		raw = v.raw()
		last = newScreen(v.rows, v.cols)
		last.feed(raw)
		if ok(last) {
			return last
		}
		time.Sleep(15 * time.Millisecond)
	}
	failScreen(v.t, "timeout "+why, last, raw)
	return nil
}

func (v *ptyView) write(keys string) {
	v.t.Helper()
	v.mu.Lock()
	v.mark = v.buf.Len()
	v.mu.Unlock()
	if _, err := v.pt.Write([]byte(keys)); err != nil {
		v.t.Fatal(err)
	}
}

// splitKeys breaks a byte string into terminal keys. An arrow is one key.
func splitKeys(s string) []string {
	b := []byte(s)
	var out []string
	for i := 0; i < len(b); {
		if b[i] == 0x1b && i+2 < len(b) && b[i+1] == '[' {
			f := b[i+2]
			if f == 'A' || f == 'B' || f == 'C' || f == 'D' || f == 'F' || f == 'H' {
				out = append(out, string(b[i:i+3]))
				i += 3
				continue
			}
		}
		_, n := utf8.DecodeRune(b[i:])
		if n < 1 {
			n = 1
		}
		out = append(out, string(b[i:i+n]))
		i += n
	}
	return out
}

// typeKeys writes one key at a time. The next key is sent only after the
// screen or the raw stream shows the app handled the previous one. done is
// checked on the rendered screen after the last key.
func (v *ptyView) typeKeys(keys string, done func(*screen) bool) {
	v.t.Helper()
	parts := splitKeys(keys)
	if len(parts) == 0 {
		if done != nil {
			v.expect("screen", done)
		}
		return
	}
	for i, part := range parts {
		beforeN := len(v.raw())
		beforeDump := v.shot().dump()
		v.write(part)
		last := i == len(parts)-1
		v.expect(strconv.Quote(part), func(sc *screen) bool {
			moved := sc.dump() != beforeDump || len(v.raw()) > beforeN
			if !moved {
				return false
			}
			if last && done != nil {
				return done(sc)
			}
			return true
		})
	}
}

func screenHas(s string) func(*screen) bool {
	return func(sc *screen) bool { return strings.Contains(sc.dump(), s) }
}

func screenHasAll(parts ...string) func(*screen) bool {
	return func(sc *screen) bool {
		d := sc.dump()
		for _, p := range parts {
			if !strings.Contains(d, p) {
				return false
			}
		}
		return true
	}
}

func startView(t *testing.T, pt *os.File, rows, cols int) *ptyView {
	t.Helper()
	v := &ptyView{t: t, pt: pt, mu: &sync.Mutex{}, buf: &bytes.Buffer{}, rows: rows, cols: cols}
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := pt.Read(tmp)
			if n > 0 {
				v.mu.Lock()
				v.buf.Write(tmp[:n])
				v.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return v
}

func screenPrompt(sc *screen) string {
	if sc == nil || sc.r < 0 || sc.r >= sc.h {
		return ""
	}
	end := sc.c
	if end < 0 {
		end = 0
	}
	if end > len(sc.cell[sc.r]) {
		end = len(sc.cell[sc.r])
	}
	return strings.TrimRight(string(sc.cell[sc.r][:end]), " ")
}

func TestReadlineSubmittedOnceOnPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "1" {
		rd, err := NewReadline(nil, func() string { return "cwd · branch" }, nil, nil)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		line, err := rd.ReadLine(context.Background(), "› ")
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		if line != "hii" {
			os.Stderr.WriteString("got " + line)
			os.Exit(1)
		}
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestReadlineSubmittedOnceOnPTY")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=1")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	v := startView(t, ptmx, 24, 80)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	v.typeKeys("hii", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "hii") })
	v.typeKeys("\n", func(sc *screen) bool { return strings.Contains(sc.dump(), "hii") })
	deadline := time.Now().Add(ptyTimeout())
	var waitErr error
	done := make(chan struct{})
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		failScreen(t, "child still running", v.shot(), v.raw())
	}
	raw := v.raw()
	plain := ansiRe.ReplaceAll(raw, nil)
	plain = bytes.ReplaceAll(plain, []byte{'\r'}, nil)
	n := strings.Count(string(plain), "hii")
	if n != 1 {
		failScreen(t, "submitted text appeared "+strconv.Itoa(n)+" times", v.shot(), raw)
	}
	if waitErr != nil {
		failScreen(t, "child: "+waitErr.Error(), v.shot(), raw)
	}
}

func TestSlashMenuDownReplacesLineOnPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "slash-down" {
		rd, err := NewReadline(nil, func() string { return "FOOTER_MARK · branch" }, nil, commands.Default())
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		line, err := rd.ReadLine(context.Background(), "› ")
		if err != nil && err != ErrInterrupt && err != ErrEOF {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		os.Stdout.WriteString("SUBMIT=" + line + "\n")
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestSlashMenuDownReplacesLineOnPTY")
	logf := filepath.Join(t.TempDir(), "slash.log")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=slash-down", "COLUMNS=80", "INPUTRC=/dev/null", "KONSOLAI_SLASH_LOG="+logf)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	v := startView(t, ptmx, 24, 80)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	v.typeKeys("/", screenHas("/help"))
	v.typeKeys("\x1b[B", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/help") })
	v.typeKeys("\r", func(sc *screen) bool { return strings.Contains(sc.dump(), "SUBMIT=/help") })
	if !strings.Contains(v.shot().dump(), "SUBMIT=/help") {
		logb, _ := os.ReadFile(logf)
		loga, _ := os.ReadFile(logf + ".after")
		failScreen(t, "submitted\nlog="+string(logb)+" after="+string(loga), v.shot(), v.raw())
	}
}

func TestFrameSlashBackspaceClearsMenu(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "slash" {
		runSlashChild()
	}
	sc := driveSlashPTY(t, "TestFrameSlashBackspaceClearsMenu", []byte("/\x7f"))
	if leftover := menuLeftover(sc); leftover != "" {
		t.Fatalf("menu leftover %q:\n%s", leftover, sc.dump())
	}
	if gap := frameGap(sc, "FOOTER_MARK"); gap > 1 {
		t.Fatalf("blank block below footer (%d empty rows in frame):\n%s", gap, sc.dump())
	}
	row, col, ok := promptCursor(sc)
	if !ok {
		t.Fatalf("prompt line missing:\n%s", sc.dump())
	}
	if sc.r != row {
		t.Fatalf("cursor row %d want %d (› line), col %d want ~%d\n%s", sc.r, row, sc.c, col, sc.dump())
	}
	if sc.c < col-1 || sc.c > col+1 {
		t.Fatalf("cursor col %d want %d (right after › )\n%s", sc.c, col, sc.dump())
	}
}

func TestFrameCursorAfterMenuClose(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "slash" {
		runSlashChild()
	}
	sc := driveSlashPTY(t, "TestFrameCursorAfterMenuClose", []byte("/\x7f"))
	row, col, ok := promptCursor(sc)
	if !ok {
		t.Fatalf("prompt line missing:\n%s", sc.dump())
	}
	if leftover := menuLeftover(sc); leftover != "" {
		t.Fatalf("menu leftover %q:\n%s", leftover, sc.dump())
	}
	if sc.r != row {
		t.Fatalf("cursor row %d want %d (› line)\n%s", sc.r, row, sc.dump())
	}
	if sc.c < col-1 || sc.c > col+1 {
		t.Fatalf("cursor col %d want %d (right after › )\n%s", sc.c, col, sc.dump())
	}
}

func TestFrameNoPartialRuleAfterTurn(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "turn" {
		rd, err := NewReadline(nil, func() string { return "FOOTER_MARK · branch" }, nil, nil)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		line, err := rd.ReadLine(context.Background(), "› ")
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		if fe, ok := rd.(interface{ EraseFrame(io.Writer, string) }); ok {
			fe.EraseFrame(os.Stdout, line)
		}
		echoInput(os.Stdout, line)
		os.Stdout.WriteString("TURN_DONE\n")
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestFrameNoPartialRuleAfterTurn")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=turn", "COLUMNS=80")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	v := startView(t, ptmx, 24, 80)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	v.typeKeys("hello", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "hello") })
	v.typeKeys("\n", screenHas("TURN_DONE"))
	_ = cmd.Wait()
	sc := v.shot()
	if short := partialRules(sc, 80); len(short) > 0 {
		failScreen(t, "partial rules in scrollback", sc, v.raw())
	}
	if !strings.Contains(sc.dump(), "hello") {
		failScreen(t, "missing echo", sc, v.raw())
	}
}

func runSlashChild() {
	rd, err := NewReadline(Completer(commands.Default(), func() string { return os.TempDir() }), func() string {
		return "FOOTER_MARK · branch"
	}, nil, commands.Default())
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	_, err = rd.ReadLine(context.Background(), "› ")
	if err != nil && err != ErrInterrupt && err != ErrEOF {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func driveSlashPTY(t *testing.T, testName string, keys []byte) *screen {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run="+testName)
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=slash", "COLUMNS=80")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	v := startView(t, ptmx, 24, 80)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	if len(keys) > 0 {
		v.typeKeys(string(keys[:1]), func(sc *screen) bool {
			if keys[0] == '/' {
				return strings.Contains(sc.dump(), "/help")
			}
			return true
		})
	}
	if len(keys) > 1 {
		v.typeKeys(string(keys[1:]), func(sc *screen) bool {
			return menuLeftover(sc) == ""
		})
	}
	return v.shot()
}

func promptCursor(sc *screen) (row, col int, ok bool) {
	for i, rowCells := range sc.cell {
		plain := strings.TrimRight(string(rowCells), " ")
		idx := strings.IndexRune(plain, '›')
		if idx < 0 {
			continue
		}
		if strings.Contains(plain, "FOOTER_MARK") {
			continue
		}
		return i, idx + theme.DisplayWidth("› "), true
	}
	return 0, 0, false
}

func menuLeftover(sc *screen) string {
	for _, line := range sc.lines() {
		plain := theme.StripANSI(line)
		if strings.Contains(plain, "/help") || strings.Contains(plain, "more completion") {
			return strings.TrimSpace(plain)
		}
	}
	return ""
}

func frameGap(sc *screen, mark string) int {
	prompt, foot := -1, -1
	for i, line := range sc.lines() {
		plain := theme.StripANSI(line)
		if strings.ContainsRune(plain, '›') && !strings.Contains(plain, mark) && prompt < 0 {
			prompt = i
		}
		if strings.Contains(plain, mark) {
			foot = i
		}
	}
	if prompt < 0 || foot <= prompt {
		return 0
	}
	gap := 0
	for _, line := range sc.lines()[prompt+1 : foot] {
		if strings.TrimSpace(theme.StripANSI(line)) == "" {
			gap++
		}
	}
	return gap
}

func partialRules(sc *screen, width int) []int {
	var short []int
	for i, line := range sc.lines() {
		plain := strings.TrimRight(theme.StripANSI(line), " ")
		if plain == "" || !strings.Contains(plain, "─") {
			continue
		}
		n := 0
		for _, r := range plain {
			if r == '─' {
				n++
			}
		}
		if n >= 8 && n < width-1 {
			short = append(short, i)
		}
	}
	return short
}

type screen struct {
	h, w int
	r, c int
	cell [][]rune
}

func newScreen(h, w int) *screen {
	s := &screen{h: h, w: w}
	s.cell = make([][]rune, h)
	for i := range s.cell {
		s.cell[i] = make([]rune, w)
		for j := range s.cell[i] {
			s.cell[i][j] = ' '
		}
	}
	return s
}

func (s *screen) lines() []string {
	out := make([]string, s.h)
	for i, row := range s.cell {
		out[i] = strings.TrimRight(string(row), " ")
	}
	return out
}

func (s *screen) dump() string {
	return strings.Join(s.lines(), "\n")
}

func (s *screen) put(r rune) {
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

func (s *screen) scroll() {
	copy(s.cell, s.cell[1:])
	s.cell[s.h-1] = make([]rune, s.w)
	for j := range s.cell[s.h-1] {
		s.cell[s.h-1][j] = ' '
	}
}

func (s *screen) clearBelow() {
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

func (s *screen) clearLine() {
	if s.r < 0 || s.r >= s.h {
		return
	}
	for c := s.c; c < s.w; c++ {
		s.cell[s.r][c] = ' '
	}
}

func (s *screen) feed(p []byte) {
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

// csi consumes one escape. CSI is parameters (0x30-0x3F), intermediates
// (0x20-0x2F), then one final byte (0x40-0x7E). DECSCUSR is ESC [ n SP q.
func (s *screen) csi(p []byte, i int) int {
	if i+1 >= len(p) {
		return len(p)
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
		s.applyCSI(p[j], parseCSIArgs(string(p[i+2:paramEnd])))
		return j + 1
	default:
		return i + 2
	}
}

func parseCSIArgs(s string) []int {
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

func (s *screen) applyCSI(cmd byte, args []int) {
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

func TestSplitKeysKeepsArrows(t *testing.T) {
	got := splitKeys("\x1b[C\r")
	if len(got) != 2 || got[0] != "\x1b[C" || got[1] != "\r" {
		t.Fatalf("%q", got)
	}
}

func TestScreenSkipsDECSCUSR(t *testing.T) {
	sc := newScreen(2, 40)
	sc.feed([]byte("ab\x1b[1 qcd"))
	if strings.Contains(sc.dump(), "[1 q") {
		t.Fatalf("cursor sequence leaked\n%s", sc.dump())
	}
	if !strings.Contains(sc.dump(), "abcd") {
		t.Fatalf("text\n%s", sc.dump())
	}
	sc = newScreen(3, 20)
	sc.feed([]byte("\x1b[2;3Hxy"))
	if sc.r != 1 || sc.c != 4 {
		t.Fatalf("cup r=%d c=%d\n%s", sc.r, sc.c, sc.dump())
	}
	row := strings.TrimRight(string(sc.cell[1]), " ")
	if !strings.HasSuffix(row, "xy") {
		t.Fatalf("row %q", row)
	}
}

func TestPlaceholderNotAcceptedOnPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "placeholder" {
		rd, err := NewReadline(nil, func() string { return "FOOTER_MARK" }, nil, commands.Default())
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		if sp, ok := rd.(interface{ SetPlaceholder(string) }); ok {
			sp.SetPlaceholder("ask anything")
		}
		line, err := rd.ReadLine(context.Background(), "› ")
		if err != nil && err != ErrInterrupt && err != ErrEOF {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		os.Stdout.WriteString("SUBMIT=" + line + "\n")
		os.Exit(0)
	}
	cases := []struct {
		name   string
		keys   string
		submit string
	}{
		{name: "right", keys: "\x1b[C\r", submit: ""},
		{name: "end", keys: "\x1b[F\r", submit: ""},
		{name: "tab", keys: "\t\r", submit: ""},
		{name: "type", keys: "hi\r", submit: "hi"},
		{name: "slash", keys: "/\r", submit: "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plain := drivePlaceholder(t, tc.keys)
			if !strings.Contains(plain, "ask anything") {
				t.Fatalf("placeholder not shown:\n%s", plain)
			}
			if strings.Contains(plain, "SUBMIT=ask anything") {
				t.Fatalf("placeholder submitted:\n%s", plain)
			}
			if !strings.Contains(plain, "SUBMIT="+tc.submit+"\n") {
				t.Fatalf("submit want %q\n%s", tc.submit, plain)
			}
			if tc.name == "slash" && !strings.Contains(plain, "/help") {
				t.Fatalf("slash menu did not open:\n%s", plain)
			}
		})
	}
}

func TestCtrlLCallsClearHook(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "ctrl-l" {
		rd, err := NewReadline(nil, func() string { return "FOOTER_MARK" }, nil, nil)
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		if clr, ok := rd.(interface{ SetOnClear(func()) }); ok {
			clr.SetOnClear(func() { os.Stdout.WriteString("COMPACT_ONLY\n") })
		}
		_, _ = rd.ReadLine(context.Background(), "› ")
		os.Exit(0)
	}
	plain := driveKeys(t, "TestCtrlLCallsClearHook", "ctrl-l", "\x0c", "COMPACT_ONLY")
	if !strings.Contains(plain, "COMPACT_ONLY") {
		t.Fatalf("ctrl-l did not clear:\n%s", plain)
	}
	if strings.Contains(plain, "╭") {
		t.Fatalf("ctrl-l drew a welcome card:\n%s", plain)
	}
}

func drivePlaceholder(t *testing.T, keys string) string {
	t.Helper()
	return driveKeys(t, "TestPlaceholderNotAcceptedOnPTY", "placeholder", keys, "SUBMIT=")
}

func driveKeys(t *testing.T, testName, child, keys, done string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD="+child, "COLUMNS=80", "INPUTRC=/dev/null")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	v := startView(t, ptmx, 24, 80)
	ready := "›"
	if strings.Contains(done, "SUBMIT") && strings.Contains(keys, "ask") {
		ready = "ask anything"
	}
	v.expect("prompt", func(sc *screen) bool {
		d := sc.dump()
		return strings.Contains(d, ready) || strings.Contains(d, "ask anything") || strings.Contains(d, "FOOTER_MARK")
	})
	// Right and End are ambiguous CSI prefixes until the following byte is
	// buffered. Send that gesture in one write, then wait for the screen.
	if keys == "\x1b[C\r" || keys == "\x1b[F\r" {
		v.write(keys)
		v.expect(done, screenHas(done))
	} else {
		parts := splitKeys(keys)
		for i, part := range parts {
			var step func(*screen) bool
			switch {
			case i == len(parts)-1:
				step = screenHas(done)
			case part == "/":
				step = screenHas("/help")
			}
			v.typeKeys(part, step)
		}
	}
	return ansiRe.ReplaceAllString(strings.ReplaceAll(string(v.raw()), "\r", ""), "")
}
