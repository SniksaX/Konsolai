package repl

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/creack/pty"
)

const (
	argRows = 48
	argCols = 120
)

func TestPTYSetListsModelThemeSteps(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetListsModelThemeSteps")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set"))
	s.wait("name of the main chat model", 3*time.Second)
	plain := s.plain()
	for _, want := range []string{"model", "theme", "amber"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
	s.until("act turn step cap", 20, "\x1b[B")
	if !strings.Contains(s.plain(), "15") {
		t.Fatalf("steps current missing\n%s", s.plain())
	}
	s.write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	s.quiet("/set")
	t.Log("pass: /set lists model, theme, and steps with descriptions and current values")
}

func TestPTYSetModelEnterOpensRoles(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetModelEnterOpensRoles")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set "))
	s.wait("name of the main chat model", 3*time.Second)
	s.write([]byte("\x1b[B"))
	s.wait("/set model", 2*time.Second)
	s.write([]byte("\r"))
	s.wait("discover", 3*time.Second)
	plain := s.plain()
	for _, want := range []string{"main", "fast", "add", "remove", "discover"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, s.plain())
		}
	}
	s.write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	s.quiet("/set model")
	t.Log("pass: enter on model opens main, fast, add, remove, discover")
}

func TestPTYSetFastRoleRestores(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetFastRoleRestores")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set "))
	s.wait("name of the main chat model", 3*time.Second)
	s.write([]byte("\x1b[B\r"))
	s.wait("discover", 3*time.Second)
	s.write([]byte("\x1b[B\x1b[B\r"))
	s.wait("extra", 3*time.Second)
	s.write([]byte("\x1b[B\x1b[B\x1b[B\r"))
	s.wait("from the next turn", 3*time.Second)
	body := s.readCfg()
	if !strings.Contains(body, "extra") {
		t.Fatalf("fast role did not change\n%s\n%s", body, s.plain())
	}
	from := s.mark()
	s.write([]byte("/set model fast off\r"))
	s.waitNew(from, "from the next turn", 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.readCfg(), "extra") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	body = s.readCfg()
	if strings.Contains(body, "extra") {
		t.Fatalf("fast role not restored\n%s", body)
	}
	s.quiet("")
	t.Log("pass: fast role changed to extra and was restored")
}

func TestPTYSetStepsFreeText(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetStepsFreeText")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set steps "))
	s.wait("current 15", 3*time.Second)
	plain := s.plain()
	for _, want := range []string{"default 15", "range"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
	s.write([]byte("20\r"))
	s.wait("from the next turn", 3*time.Second)
	if !strings.Contains(s.readCfg(), "20") {
		t.Fatalf("steps not saved\n%s", s.readCfg())
	}
	from := s.mark()
	s.write([]byte("/set steps default\r"))
	s.waitNew(from, "from the next turn", 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !strings.Contains(s.readCfg(), "20") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if strings.Contains(s.readCfg(), "20") {
		t.Fatalf("steps not restored\n%s", s.readCfg())
	}
	s.quiet("")
	t.Log("pass: steps free-text saved 20 and default restored it")
}

func TestPTYSetThemeEsc(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetThemeEsc")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set theme "))
	s.wait("glacier", 3*time.Second)
	plain := s.plain()
	for _, name := range []string{"amber", "glacier", "nebula", "jade", "rose", "mono"} {
		if !strings.Contains(plain, name) {
			t.Fatalf("missing theme %s\n%s", name, plain)
		}
	}
	if !strings.Contains(plain, "current") {
		t.Fatalf("current theme not marked\n%s", plain)
	}
	s.write([]byte("\x1b"))
	s.wait("/set theme", 2*time.Second)
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(s.screenText(), "nebula") {
		t.Fatalf("theme list stayed\n%s", s.screenText())
	}
	s.write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	row := s.cursorRow()
	if strings.Contains(row, "/set") {
		t.Fatalf("line not cleared: %q\n%s", row, s.screenText())
	}
	if strings.Contains(s.readCfg(), "glacier") {
		t.Fatalf("theme changed\n%s", s.readCfg())
	}
	s.quiet("")
	t.Log("pass: theme list marked current; esc returned to /set theme; esc cleared the line")
}

func TestPTYSetTabCompletes(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetTabCompletes")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set th"))
	s.wait("think", 3*time.Second)
	plain := s.plain()
	if !strings.Contains(plain, "theme") || !strings.Contains(plain, "think") {
		t.Fatalf("filter\n%s", plain)
	}
	from := s.mark()
	s.write([]byte("\t"))
	s.waitNew(from, "/set theme ", 3*time.Second)
	if !strings.Contains(s.plain()[from:], "mono") {
		t.Fatalf("theme level did not open\n%s", s.plain()[from:])
	}
	s.write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	s.quiet("/set theme")
	t.Log("pass: /set th filtered to theme and think; tab opened theme")
}

func TestPTYSetQuestionDetails(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetQuestionDetails")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/set "))
	s.wait("name of the main chat model", 3*time.Second)
	s.write([]byte("\x1b[B?"))
	s.wait("roles.main", 3*time.Second)
	plain := s.plain()
	for _, want := range []string{"path", "type", "default", "current", "source", "when"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
	s.write([]byte("?"))
	time.Sleep(250 * time.Millisecond)
	if strings.Contains(s.screenText(), "roles.main") {
		t.Fatalf("details stayed\n%s", s.screenText())
	}
	s.write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	s.quiet("/set")
	t.Log("pass: ? showed /set details and ? hid them")
}

func TestPTYCopyArgs(t *testing.T) {
	s := startArgREPL(t, "TestPTYCopyArgs")
	defer s.stop()
	s.wait("main-model", 4*time.Second)
	s.write([]byte("/copy "))
	s.wait("Copy the last answer", 3*time.Second)
	plain := s.plain()
	for _, want := range []string{"answer", "all", "code", "cmd", "Copy the last prompt and answer", "Copy a code block", "Copy a command"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
	s.write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	s.quiet("/copy")
	t.Log("pass: /copy lists answer, all, code, and cmd")
}

type argREPL struct {
	t   *testing.T
	pt  *os.File
	cmd *exec.Cmd
	cfg string
	mu  sync.Mutex
	buf bytes.Buffer
}

func startArgREPL(t *testing.T, testName string) *argREPL {
	t.Helper()
	if os.Getenv("KONSOLAI_PTY_CHILD") == "argmenu" {
		argMenuChild()
		return nil
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "src", "app")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("defaults: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD=argmenu",
		"HOME="+home,
		"KONSOLAI_ARG_CWD="+dir,
		"KONSOLAI_ARG_CFG="+cfg,
		"COLUMNS=120",
		"LINES=48",
		"INPUTRC=/dev/null",
		"NO_COLOR=1",
	)
	pt, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	_ = pty.Setsize(pt, &pty.Winsize{Rows: argRows, Cols: argCols})
	s := &argREPL{t: t, pt: pt, cmd: cmd, cfg: cfg}
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := pt.Read(tmp)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(tmp[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *argREPL) stop() {
	if s == nil {
		return
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pt.Close()
	_ = s.cmd.Wait()
}

func (s *argREPL) write(b []byte) {
	s.t.Helper()
	time.Sleep(80 * time.Millisecond)
	if _, err := s.pt.Write(b); err != nil {
		s.t.Fatal(err)
	}
}

func (s *argREPL) raw() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.buf.Bytes()...)
}

func (s *argREPL) plain() string {
	return ansiRe.ReplaceAllString(strings.ReplaceAll(string(s.raw()), "\r", ""), "")
}

func (s *argREPL) mark() int { return len(s.plain()) }

func (s *argREPL) wait(needle string, d time.Duration) {
	s.t.Helper()
	s.waitNew(0, needle, d)
}

func (s *argREPL) waitNew(from int, needle string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		p := s.plain()
		if from > len(p) {
			from = 0
		}
		if strings.Contains(p[from:], needle) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("timeout waiting for %q\n%s", needle, s.plain())
}

func (s *argREPL) until(needle string, max int, key string) {
	s.t.Helper()
	for i := 0; i < max; i++ {
		if strings.Contains(s.plain(), needle) {
			return
		}
		s.write([]byte(key))
		time.Sleep(40 * time.Millisecond)
	}
	s.t.Fatalf("never saw %q\n%s", needle, s.plain())
}

func (s *argREPL) readCfg() string {
	b, err := os.ReadFile(s.cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *argREPL) shot() *screen {
	sc := newScreen(argRows, argCols)
	sc.feed(s.raw())
	return sc
}

func (s *argREPL) screenText() string {
	return s.shot().dump()
}

func (s *argREPL) cursorRow() string {
	sc := s.shot()
	if sc.r < 0 || sc.r >= sc.h {
		return ""
	}
	return strings.TrimRight(sc.lines()[sc.r], " ")
}

func (s *argREPL) quiet(input string) {
	s.t.Helper()
	time.Sleep(180 * time.Millisecond)
	sc := s.shot()
	dump := sc.dump()
	if sc.r < 0 || sc.r >= sc.h {
		s.t.Fatalf("cursor row %d\n%s", sc.r, dump)
	}
	below := ""
	if sc.r+1 < sc.h {
		below = strings.Join(sc.lines()[sc.r+1:], "\n")
	}
	for _, bad := range []string{"↓ ", "↑ ", "next/run", "select · enter"} {
		if strings.Contains(below, bad) {
			s.t.Fatalf("leftover menu %q\n%s", bad, dump)
		}
	}
	if sc.c > len(sc.cell[sc.r]) {
		s.t.Fatalf("cursor col %d\n%s", sc.c, dump)
	}
	before := strings.TrimRight(string(sc.cell[sc.r][:sc.c]), " ")
	if !strings.HasSuffix(before, "›") && !strings.Contains(before, "›") {
		s.t.Fatalf("cursor is not on the prompt %q\n%s", before, dump)
	}
	if input != "" && !strings.Contains(before, strings.TrimRight(input, " ")) {
		s.t.Fatalf("cursor row %q want input %q\n%s", before, input, dump)
	}
	// The cursor sits on the cell just after the prompt and the typed input.
	if sc.c < 2 {
		s.t.Fatalf("cursor col %d before the prompt\n%s", sc.c, dump)
	}
}

func argMenuChild() {
	dir := os.Getenv("KONSOLAI_ARG_CWD")
	cfgPath := os.Getenv("KONSOLAI_ARG_CFG")
	prof := config.Builtin()
	prof.SetMain("http://127.0.0.1:1/v1", "main-model", false)
	prof.Models["extra"] = config.Model{Endpoint: "http://127.0.0.1:1/v1", Model: "extra-model"}
	prof.Roles.Fast = ""
	prof.Roles.Embed = ""
	prof.Roles.Rerank = ""
	prof.Database.URL = ""
	prof.RAG.Enabled = false
	prof.UI.ClearOnStart = false
	prof.UI.Banner = false
	prof.UI.Welcome = "minimal"
	prof.UI.ProbeDeadlineMS = 80
	prof.UI.ProbeTimeoutMS = 40
	env := &commands.Env{
		Profile:    prof,
		Out:        os.Stdout,
		Err:        os.Stderr,
		Cwd:        dir,
		Version:    "vtest",
		ConfigPath: cfgPath,
	}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Version: "vtest", Model: "main-model",
		Out: os.Stdout, Err: os.Stderr,
	})
	os.Exit(code)
}
