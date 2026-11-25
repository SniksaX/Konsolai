package repl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
)

// TestSetMenuFits24Rows proves the interactive /set picker caps its painted
// height at a 24-row terminal and scrolls instead of painting the whole list.
// It then navigates to ui.welcome, picks a value, and checks the write.
func TestSetMenuFits24Rows(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "set-menu" {
		setMenuChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "src", "app")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	init := exec.Command("git", "init", "-b", "dev", dir)
	init.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("defaults:\n  ui:\n    welcome: split\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSetMenuFits24Rows$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD=set-menu",
		"HOME="+home,
		"KONSOLAI_THEME_CWD="+dir,
		"KONSOLAI_THEME_CFG="+cfg,
		"COLUMNS=120",
		"LINES=24",
		"INPUTRC=/dev/null",
		"NO_COLOR=1",
	)
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
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 120})
	v := startView(t, ptmx, 24, 120)
	v.expect("welcome", screenHas("main-model"))
	v.typeKeys("/set", func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "/set") })
	// Enter submits the bare /set and opens the interactive picker. Only a
	// windowed menu shows the welcome row and the "rows below" marker at once
	// on a 24-row screen.
	v.typeKeys("\r", screenHasAll("welcome", "↓ "))
	if strings.Contains(v.shot().dump(), "↑ ") {
		failScreen(t, "the first paint should not mark rows above", v.shot(), v.raw())
	}
	// Scroll far down, then back to the top: the window follows the selection
	// and the markers stay on screen.
	v.typeKeys(strings.Repeat("\x1b[B", 40), nil)
	v.expect("scroll marker above", screenHas("↑ "))
	v.typeKeys(strings.Repeat("\x1b[A", 40), nil)
	v.expect("back at the top", func(sc *screen) bool {
		return strings.Contains(sc.dump(), "welcome") && !strings.Contains(sc.dump(), "↑ ")
	})
	// ui.welcome is the third selectable row: down twice opens its value list.
	v.typeKeys("\x1b[B\x1b[B", nil)
	v.typeKeys("\r", screenHas("minimal"))
	v.typeKeys("\x1b[B", nil)
	v.typeKeys("\r", func(*screen) bool {
		body, err := os.ReadFile(cfg)
		return err == nil && strings.Contains(string(body), "frame")
	})
	body, err := os.ReadFile(cfg)
	if err != nil || !strings.Contains(string(body), "frame") {
		failScreen(t, "welcome not updated\n"+string(body), v.shot(), v.raw())
	}
}
