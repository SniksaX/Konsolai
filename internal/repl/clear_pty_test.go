package repl

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/creack/pty"
)

func TestClearHeaderOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "clear" {
		clearChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	t.Run("full", func(t *testing.T) {
		sc := driveClear(t, "full", false, "/clear\r", func(sc *screen) bool {
			d := sc.dump()
			return stringsCount(d, "█") > 3 && stringsHas(d, "›")
		})
		if stringsCount(sc.dump(), "█") < 3 {
			t.Fatalf("banner left the screen\n%s", sc.dump())
		}
	})
	t.Run("compact", func(t *testing.T) {
		sc := driveClear(t, "compact", false, "/clear\r", func(sc *screen) bool {
			d := sc.dump()
			return stringsHas(d, "KONSOLAI") && stringsCount(d, "█") == 0 && stringsHas(d, "›")
		})
		if stringsCount(sc.dump(), "█") != 0 || !stringsHas(sc.dump(), "KONSOLAI") {
			t.Fatalf("compact\n%s", sc.dump())
		}
	})
	t.Run("none", func(t *testing.T) {
		sc := driveClear(t, "none", false, "/clear\r", func(sc *screen) bool {
			d := sc.dump()
			return stringsHas(d, "›") && !stringsHas(d, "KONSOLAI") && stringsCount(d, "█") == 0
		})
		if stringsHas(sc.dump(), "KONSOLAI") || stringsCount(sc.dump(), "█") != 0 {
			t.Fatalf("none\n%s", sc.dump())
		}
	})
	t.Run("shell", func(t *testing.T) {
		sc := driveClear(t, "full", false, "", func(sc *screen) bool {
			d := sc.dump()
			return stringsCount(d, "█") > 3 && stringsHas(d, "$")
		})
		if stringsCount(sc.dump(), "█") < 3 {
			t.Fatalf("shell clear\n%s", sc.dump())
		}
	})
}

func TestPinnedHeaderOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "clear" {
		clearChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	cmd, ptmx := startClear(t, "full", true)
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	v := startView(t, ptmx, 40, 120)
	v.expect("prompt", screenHas("›"))
	setRe := regexp.MustCompile(`\x1b\[\d+;\d+r`)
	if !setRe.Match(v.raw()) {
		t.Fatalf("region was not set\n%s", rawTail(v.raw()))
	}
	if err := cmd.Process.Signal(syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(ptyTimeout())
	for time.Now().Before(deadline) && !bytes.Contains(v.raw(), []byte("\x1b[r")) {
		time.Sleep(15 * time.Millisecond)
	}
	if !bytes.Contains(v.raw(), []byte("\x1b[r")) {
		t.Fatalf("resize did not reset the region\n%s", rawTail(v.raw()))
	}
	v.typeKeys("/exit\r", nil)
	_ = cmd.Wait()
	cmd.Process = nil
	deadline = time.Now().Add(ptyTimeout())
	for time.Now().Before(deadline) && bytes.Count(v.raw(), []byte("\x1b[r")) < 2 {
		time.Sleep(15 * time.Millisecond)
	}
	if bytes.Count(v.raw(), []byte("\x1b[r")) < 1 || !setRe.Match(v.raw()) {
		t.Fatalf("exit region\n%s", rawTail(v.raw()))
	}
}

func driveClear(t *testing.T, mode string, pin bool, keys string, done func(*screen) bool) *screen {
	t.Helper()
	cmd, ptmx := startClear(t, mode, pin)
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	v := startView(t, ptmx, 40, 120)
	v.expect("prompt", screenHas("›"))
	if keys == "" {
		v.write("/cmd\r")
		v.expect("shell", screenHas("$"))
		v.write("clear\r")
	} else {
		v.write(keys)
	}
	v.expect("cleared", func(sc *screen) bool {
		return bytes.Contains(v.raw(), []byte("\x1b[3J")) && done(sc)
	})
	return v.shot()
}

func startClear(t *testing.T, mode string, pin bool) (*exec.Cmd, *os.File) {
	t.Helper()
	pinVal := "0"
	if pin {
		pinVal = "1"
	}
	cmd := exec.Command(os.Args[0], "-test.run=^Test(ClearHeader|PinnedHeader)OnPTY$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD=clear",
		"KONSOLAI_CLEAR_MODE="+mode,
		"KONSOLAI_PIN="+pinVal,
		"COLUMNS=120",
		"LINES=40",
		"INPUTRC=/dev/null",
		"HOME="+t.TempDir(),
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})
	return cmd, ptmx
}

func clearChild() {
	prof := config.Builtin()
	prof.SetMain("http://127.0.0.1:1/v1", "main-model", false)
	prof.Roles.Fast = ""
	prof.Roles.Embed = ""
	prof.Roles.Rerank = ""
	prof.RAG.Enabled = false
	prof.Database.URL = ""
	prof.UI.Banner = true
	prof.UI.ClearOnStart = false
	prof.UI.Welcome = "split"
	prof.UI.ClearHeader = os.Getenv("KONSOLAI_CLEAR_MODE")
	prof.UI.PinnedHeader = os.Getenv("KONSOLAI_PIN") == "1"
	dir, _ := os.Getwd()
	env := &commands.Env{
		Profile: prof, Out: os.Stdout, Err: os.Stderr, Cwd: dir, Version: "vtest",
	}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Version: "vtest", Model: "main-model",
		Out: os.Stdout, Err: os.Stderr,
	})
	os.Exit(code)
}

func stringsHas(s, part string) bool {
	return bytes.Contains([]byte(s), []byte(part))
}

func stringsCount(s, part string) int {
	return bytes.Count([]byte(s), []byte(part))
}
