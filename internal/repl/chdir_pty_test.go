package repl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/creack/pty"
)

func TestCdSwitchesFooterOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "chdir" {
		chdirFooterChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	a := t.TempDir()
	b := t.TempDir()
	writeRepo(t, a, "alpha", "OLD_MARKER")
	writeRepo(t, b, "beta", "NEW_MARKER")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCdSwitchesFooterOnPTY$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD=chdir",
		"KONSOLAI_CHDIR_A="+a,
		"KONSOLAI_CHDIR_B="+b,
		"COLUMNS=120",
		"LINES=40",
		"INPUTRC=/dev/null",
		"HOME="+t.TempDir(),
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
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})
	v := startView(t, ptmx, 40, 120)
	v.expect("prompt", screenHas("›"))
	v.typeKeys("/cmd\r", screenHas("$"))
	v.typeKeys("cd "+b+"\r", screenHas("now in "+filepath.Base(b)+" (beta)"))
	v.typeKeys("exit\r", func(sc *screen) bool {
		for _, line := range sc.lines() {
			if strings.Contains(line, "beta") && !strings.Contains(line, "now in") && strings.Contains(line, "›") {
				return false
			}
			if strings.Contains(line, "beta") && !strings.Contains(line, "now in") {
				return true
			}
		}
		return false
	})
	v.typeKeys("/exit\r", nil)
}

func chdirFooterChild() {
	a := os.Getenv("KONSOLAI_CHDIR_A")
	prof := config.Builtin()
	prof.SetMain("http://127.0.0.1:1/v1", "main-model", false)
	prof.Roles.Fast = ""
	prof.Roles.Embed = ""
	prof.Roles.Rerank = ""
	prof.RAG.Enabled = false
	prof.Database.URL = ""
	prof.UI.Banner = false
	prof.UI.ClearOnStart = false
	env := &commands.Env{
		Profile: prof, Out: os.Stdout, Err: os.Stderr, Cwd: a, Version: "vtest",
	}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Version: "vtest", Model: "main-model",
		Out: os.Stdout, Err: os.Stderr,
	})
	os.Exit(code)
}
