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
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set", screenHasAll("name of the main chat model", "theme", "amber"))
	s.until("act turn step cap", 20, "\x1b[B")
	if !strings.Contains(s.screenText(), "15") {
		s.fail("steps current missing")
	}
	s.view().typeKeys("\x1b", func(sc *screen) bool {
		return strings.Contains(screenPrompt(sc), "/set") && !strings.Contains(belowPrompt(sc), "select · enter")
	})
	s.quiet("/set")
	t.Log("pass: /set lists model, theme, and steps with descriptions and current values")
}

func TestPTYSetModelEnterOpensRoles(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetModelEnterOpensRoles")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set ", screenHas("theme"))
	s.view().typeKeys("\x1b[B", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/set model") })
	s.view().typeKeys("\r", screenHasAll("main", "fast", "add", "remove", "discover"))
	s.view().typeKeys("\x1b", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/set model") })
	s.quiet("/set model")
	t.Log("pass: enter on model opens main, fast, add, remove, discover")
}

func TestPTYSetFastRoleRestores(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetFastRoleRestores")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set ", screenHas("theme"))
	s.view().typeKeys("\x1b[B", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/set model") })
	s.view().typeKeys("\r", screenHas("discover"))
	s.view().typeKeys("\x1b[B\x1b[B\r", screenHas("extra"))
	s.view().typeKeys("\x1b[B\x1b[B\x1b[B\r", func(sc *screen) bool {
		return strings.Contains(s.readCfg(), "extra") && strings.Contains(sc.dump(), "from the next turn")
	})
	s.view().typeKeys("/set model fast off\r", func(*screen) bool {
		return !strings.Contains(s.readCfg(), "extra")
	})
	s.quiet("")
	t.Log("pass: fast role changed to extra and was restored")
}

func TestPTYSetStepsFreeText(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetStepsFreeText")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set ", screenHas("theme"))
	s.view().typeKeys("steps ", screenHasAll("current 15", "default 15", "range"))
	s.view().typeKeys("20\r", func(*screen) bool { return strings.Contains(s.readCfg(), "20") })
	s.view().typeKeys("/set steps default\r", func(*screen) bool { return !strings.Contains(s.readCfg(), "20") })
	s.quiet("")
	t.Log("pass: steps free-text saved 20 and default restored it")
}

func TestPTYSetThemeEsc(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetThemeEsc")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set ", screenHas("theme"))
	s.view().typeKeys("theme ", func(sc *screen) bool {
		d := sc.dump()
		if !strings.Contains(d, "current") {
			return false
		}
		for _, name := range []string{"amber", "glacier", "nebula", "jade", "rose", "mono"} {
			if !strings.Contains(d, name) {
				return false
			}
		}
		return true
	})
	s.view().typeKeys("\x1b", func(sc *screen) bool {
		return strings.Contains(screenPrompt(sc), "/set theme") && !strings.Contains(sc.dump(), "nebula")
	})
	s.view().typeKeys("\x1b", func(sc *screen) bool {
		return !strings.Contains(screenPrompt(sc), "/set")
	})
	if strings.Contains(s.readCfg(), "glacier") {
		t.Fatalf("theme changed\n%s", s.readCfg())
	}
	s.quiet("")
	t.Log("pass: theme list marked current; esc returned to /set theme; esc cleared the line")
}

func TestPTYSetTabCompletes(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetTabCompletes")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set ", screenHas("theme"))
	s.view().typeKeys("th", screenHasAll("theme", "think"))
	s.view().typeKeys("\t", func(sc *screen) bool {
		return strings.Contains(screenPrompt(sc), "/set theme") && strings.Contains(sc.dump(), "mono")
	})
	s.view().typeKeys("\x1b", func(sc *screen) bool {
		return strings.Contains(screenPrompt(sc), "/set theme") && !strings.Contains(sc.dump(), "mono")
	})
	s.quiet("/set theme")
	t.Log("pass: /set th filtered to theme and think; tab opened theme")
}

func TestPTYSetQuestionDetails(t *testing.T) {
	s := startArgREPL(t, "TestPTYSetQuestionDetails")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/set ", screenHas("name of the main chat model"))
	s.view().typeKeys("\x1b[B", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/set model") })
	s.view().typeKeys("?", screenHasAll("roles.main", "path", "type", "default", "current", "source", "when"))
	s.view().typeKeys("?", func(sc *screen) bool { return !strings.Contains(sc.dump(), "roles.main") })
	s.view().typeKeys("\x1b", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/set") })
	s.quiet("/set")
	t.Log("pass: ? showed /set details and ? hid them")
}

func TestPTYCopyArgs(t *testing.T) {
	s := startArgREPL(t, "TestPTYCopyArgs")
	defer s.stop()
	s.view().expect("welcome", screenHas("main-model"))
	s.view().typeKeys("/", screenHas("/copy"))
	s.view().typeKeys("copy ", screenHasAll("answer", "all", "code", "cmd", "Copy the last prompt and answer", "Copy a code block", "Copy a command"))
	s.view().typeKeys("\x1b", func(sc *screen) bool {
		return strings.Contains(screenPrompt(sc), "/copy") && !strings.Contains(sc.dump(), "Copy the last prompt and answer")
	})
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

func (s *argREPL) view() *ptyView {
	return &ptyView{t: s.t, pt: s.pt, mu: &s.mu, buf: &s.buf, rows: argRows, cols: argCols}
}

func (s *argREPL) raw() []byte { return s.view().raw() }

func (s *argREPL) fail(why string) {
	s.t.Helper()
	failScreen(s.t, why, s.shot(), s.raw())
}

func (s *argREPL) until(needle string, max int, key string) {
	s.t.Helper()
	for i := 0; i < max; i++ {
		if strings.Contains(s.screenText(), needle) {
			return
		}
		s.view().typeKeys(key, nil)
	}
	if strings.Contains(s.screenText(), needle) {
		return
	}
	s.fail("never saw " + needle)
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

func belowPrompt(sc *screen) string {
	if sc.r+1 >= sc.h {
		return ""
	}
	return strings.Join(sc.lines()[sc.r+1:], "\n")
}

func (s *argREPL) quiet(input string) {
	s.t.Helper()
	s.view().expect("quiet prompt", func(sc *screen) bool {
		if sc.r < 0 || sc.r >= sc.h || sc.c < 2 || sc.c > len(sc.cell[sc.r]) {
			return false
		}
		below := belowPrompt(sc)
		for _, bad := range []string{"↓ ", "↑ ", "next/run", "select · enter"} {
			if strings.Contains(below, bad) {
				return false
			}
		}
		before := strings.TrimRight(string(sc.cell[sc.r][:sc.c]), " ")
		if !strings.Contains(before, "›") {
			return false
		}
		if input != "" && !strings.Contains(before, strings.TrimRight(input, " ")) {
			return false
		}
		return true
	})
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
