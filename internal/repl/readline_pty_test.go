package repl

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/creack/pty"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

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

	time.Sleep(80 * time.Millisecond)
	if _, err := ptmx.Write([]byte("hii\n")); err != nil {
		t.Fatal(err)
	}

	_ = ptmx.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, _ := io.ReadAll(ptmx)
	plain := ansiRe.ReplaceAll(raw, nil)
	plain = bytes.ReplaceAll(plain, []byte{'\r'}, nil)
	n := strings.Count(string(plain), "hii")
	if n != 1 {
		t.Fatalf("submitted text appeared %d times; raw=%q plain=%q", n, raw, plain)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v raw=%q", err, raw)
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
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := ptmx.Write([]byte("/")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\x1b[B")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
		mu.Unlock()
		if strings.Contains(plain, "SUBMIT=") {
			if !strings.Contains(plain, "SUBMIT=/help") {
				logb, _ := os.ReadFile(logf)
				loga, _ := os.ReadFile(logf + ".after")
				t.Fatalf("submitted %q\nlog=%q after=%q\n%s", plain, logb, loga, plain)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	plain := buf.String()
	mu.Unlock()
	t.Fatalf("timeout\n%s", ansiRe.ReplaceAllString(plain, ""))
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
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := ptmx.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if bytes.Contains(buf.Bytes(), []byte("TURN_DONE")) {
			raw := append([]byte(nil), buf.Bytes()...)
			mu.Unlock()
			_ = cmd.Wait()
			sc := newScreen(24, 80)
			sc.feed(raw)
			if short := partialRules(sc, 80); len(short) > 0 {
				t.Fatalf("partial rules in scrollback %v\n%s", short, sc.dump())
			}
			if !strings.Contains(sc.dump(), "hello") {
				t.Fatalf("missing echo:\n%s", sc.dump())
			}
			return
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	mu.Lock()
	raw := append([]byte(nil), buf.Bytes()...)
	mu.Unlock()
	t.Fatalf("timeout waiting for TURN_DONE raw=%q", raw)
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
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)
	if _, err := ptmx.Write(keys[:1]); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if len(keys) > 1 {
		if _, err := ptmx.Write(keys[1:]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	mu.Lock()
	raw := append([]byte(nil), buf.Bytes()...)
	mu.Unlock()
	sc := newScreen(24, 80)
	sc.feed(raw)
	return sc
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

func (s *screen) csi(p []byte, i int) int {
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
		args := parseCSIArgs(string(p[i+2 : j]))
		s.applyCSI(cmd, args)
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
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
		mu.Unlock()
		if strings.Contains(plain, "›") || strings.Contains(plain, "ask anything") || strings.Contains(plain, "FOOTER_MARK") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := ptmx.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
		mu.Unlock()
		if strings.Contains(plain, done) {
			return plain
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
	mu.Unlock()
	t.Fatalf("timeout waiting for %q\n%s", done, plain)
	return plain
}
