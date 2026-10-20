package repl

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/creack/pty"
)

func TestHighlightSlashAccentNotLink(t *testing.T) {
	undo := theme.Force(true, true, "amber", theme.Dark)
	defer undo()
	reg := commands.Default()
	got := highlightInput("/help now", reg)
	acc := theme.Style(theme.Accent, "/help")
	if !strings.HasPrefix(got, acc) {
		t.Fatalf("command %q want accent prefix %q", got, acc)
	}
	if strings.Contains(got, "\x1b[4;") {
		t.Fatalf("command used link underline %q", got)
	}
	if !strings.HasSuffix(got, theme.Style(theme.Primary, " now")) {
		t.Fatalf("args %q", got)
	}
	bad := highlightInput("/nope", reg)
	errStyle := theme.Style(theme.Error, "/nope")
	if !strings.Contains(bad, errStyle) {
		t.Fatalf("invalid %q want %q", bad, errStyle)
	}
	if strings.Contains(bad, theme.Style(theme.Link, "/nope")) || strings.Contains(bad, "\x1b[4;") {
		t.Fatalf("invalid used link %q", bad)
	}
	alias := highlightInput("/quit", reg)
	if !strings.HasPrefix(alias, theme.Style(theme.Accent, "/quit")) {
		t.Fatalf("alias %q", alias)
	}
	if echoBody("/help now") != theme.InputEcho("/help now") {
		t.Fatal("submitted echo changed")
	}
}

func TestHighlightSlashOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "highlight" {
		highlightChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	undo := theme.Force(true, true, "amber", theme.Dark)
	defer undo()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHighlightSlashOnPTY$")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=highlight", "INPUTRC=/dev/null", "TERM=xterm-256color")
	pt, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = pt.Close()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(pt, &pty.Winsize{Rows: 24, Cols: 80})
	v := startView(t, pt, 24, 80)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	v.typeKeys("/help", screenHas("/help"))
	raw := v.raw()
	if !bytes.Contains(raw, []byte(theme.Style(theme.Accent, "/help"))) || bytes.Contains(raw, []byte(theme.Style(theme.Link, "/help"))) || bytes.Contains(raw, []byte("\x1b[4;")) {
		failScreen(t, "pty accent", v.shot(), raw)
	}
	v.typeKeys("\x15", func(sc *screen) bool { return !strings.Contains(screenPrompt(sc), "/help") })
	v.typeKeys("/nope", screenHas("/nope"))
	raw = v.raw()
	if !bytes.Contains(raw, []byte(theme.Style(theme.Error, "/nope"))) || bytes.Contains(raw, []byte(theme.Style(theme.Link, "/nope"))) {
		failScreen(t, "invalid pty accent", v.shot(), raw)
	}
}

func highlightChild() {
	theme.Force(true, true, "amber", theme.Dark)
	rd, err := NewReadline(nil, func() string { return "" }, nil, commands.Default())
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	_, _ = rd.ReadLine(context.Background(), "› ")
	os.Exit(0)
}
