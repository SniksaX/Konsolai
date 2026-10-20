package repl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/creack/pty"
)

func TestAtMenuOnPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "at" {
		runAtChild()
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent.go"), []byte("package agent\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("NOTES_LINE_ONE\nsecond\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v := startAt(t, dir)

	v.typeKeys("@", func(sc *screen) bool {
		d := sc.dump()
		return strings.Contains(d, "internal/") && strings.Contains(d, "dir ·") && strings.Contains(d, "notes.txt")
	})
	v.write("\x01\x0b")
	v.see("clear", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.TrimSpace(text) == ""
	})
	v.typeKeys("@int", func(sc *screen) bool {
		d := sc.dump()
		text, _, ok := inputState(sc)
		return ok && text == "@int" && strings.Contains(d, "internal/") && !strings.Contains(d, "notes.txt")
	})

	v.write("\x01\x0b")
	v.see("clear2", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.TrimSpace(text) == ""
	})
	v.typeKeys("@", func(sc *screen) bool { return strings.Contains(sc.dump(), "internal/") })
	v.write("\x1b[B")
	v.see("down dir", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@internal/"
	})
	v.write("\r")
	v.see("enter dir", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		d := sc.dump()
		return ok && text == "@internal/" && strings.Contains(d, "agent.go")
	})
	v.write("\x1b[B")
	v.see("down file", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.HasPrefix(text, "@internal/agent.go")
	})
	v.write("\r")
	v.see("enter file", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		d := sc.dump()
		return ok && text == "@internal/agent.go " && !strings.Contains(d, "dir ·")
	})

	v.write("\x01\x0b")
	v.see("clear3", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.TrimSpace(text) == ""
	})
	v.typeKeys("@notes", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@notes" && strings.Contains(sc.dump(), "notes.txt")
	})
	v.write("\x1b[B")
	v.see("select notes", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@notes.txt"
	})
	v.write("?")
	v.see("details", func(sc *screen) bool {
		d := sc.dump()
		return strings.Contains(d, "NOTES_LINE_ONE") && strings.Contains(d, "notes.txt")
	})
	v.write("?")
	v.see("details off", func(sc *screen) bool { return !strings.Contains(sc.dump(), "NOTES_LINE_ONE") })

	v.write("\x01\x0b")
	v.see("clear4", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.TrimSpace(text) == ""
	})
	v.typeKeys("@no", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@no" && strings.Contains(sc.dump(), "notes.txt")
	})
	v.write("\x1b[B")
	v.see("arrow notes", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@notes.txt"
	})
	v.write("\x1b")
	v.see("esc restores", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@no" && !strings.Contains(sc.dump(), "open/attach")
	})

	v.write("\x01\x0b")
	v.see("clear5", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.TrimSpace(text) == ""
	})
	v.typeKeys("@notes", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && text == "@notes"
	})
	v.write("\x1b[D")
	v.see("left in at menu", func(sc *screen) bool {
		text, cur, ok := inputState(sc)
		return ok && text == "@notes" && cur == len("@notes")-1 && !strings.ContainsAny(text, "[;~") && strings.Contains(sc.dump(), "notes.txt")
	})
}

func runAtChild() {
	rd, err := NewReadline(nil, func() string { return "FOOTER_MARK" }, nil, commands.Default())
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	dir := os.Getenv("KONSOLAI_AT_DIR")
	if s, ok := rd.(interface{ SetFileRoot(func() string) }); ok {
		s.SetFileRoot(func() string { return dir })
	}
	_, err = rd.ReadLine(context.Background(), "› ")
	if err != nil && err != ErrInterrupt && err != ErrEOF {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func startAt(t *testing.T, dir string) *ptyView {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAtMenuOnPTY$")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=at", "KONSOLAI_AT_DIR="+dir, "COLUMNS=100", "INPUTRC=/dev/null")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	})
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 100})
	v := startView(t, ptmx, 24, 100)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	return v
}
