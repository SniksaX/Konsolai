package repl

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/creack/pty"
	"github.com/reeflective/readline/inputrc"
)

func TestSlashWidgetDownSelectsHelp(t *testing.T) {
	rd, err := NewReadline(nil, nil, nil, commands.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := rd.(*rlReader)
	r.sh.Line().Set([]rune("/")...)
	r.sh.Cursor().Set(1)
	r.slashDown()
	if got := string(*r.sh.Line()); got != "/help" {
		t.Fatalf("got %q open=%v sel=%d items=%d", got, r.menu.Open(), r.menu.Selected(), len(r.menu.Items()))
	}
}

func TestSlashEmacsBindsArrows(t *testing.T) {
	rd, err := NewReadline(nil, nil, nil, commands.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := rd.(*rlReader)
	binds := r.sh.Config.Binds["emacs"]
	if b := binds[inputrc.Unescape(`\e`)]; b.Action != "konsolai-esc" {
		t.Fatalf("\\e => %s", b.Action)
	}
	if b := binds[inputrc.Unescape(`\e[B`)]; b.Action != "konsolai-down" {
		t.Fatalf("\\e[B => %s", b.Action)
	}
	if b := binds[inputrc.Unescape(`\M-[B`)]; b.Action != "konsolai-down" {
		t.Fatalf("\\M-[B => %s", b.Action)
	}
	local := r.sh.Config.Binds["slash-esc"]
	if local == nil || local[inputrc.Unescape(`\e`)].Action != "konsolai-esc" {
		t.Fatal("slash-esc local map missing")
	}
}

func TestSlashPTYDownSubmitsHelp(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "slashdown" {
		rd, err := NewReadline(nil, nil, nil, commands.Default())
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		line, err := rd.ReadLine(context.Background(), "› ")
		if err != nil && err != ErrInterrupt && err != ErrEOF {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		if p := os.Getenv("KONSOLAI_LINE_FILE"); p != "" {
			_ = os.WriteFile(p, []byte(line), 0600)
		}
		if line != "/help" {
			os.Exit(2)
		}
		os.Exit(0)
	}
	lineFile := t.TempDir() + "/line"
	cmd := exec.Command(os.Args[0], "-test.run=TestSlashPTYDownSubmitsHelp")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=slashdown", "KONSOLAI_LINE_FILE="+lineFile)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	time.Sleep(250 * time.Millisecond)
	if _, err := ptmx.Write([]byte("/")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var raw []byte
	buf := make([]byte, 4096)
	seen := false
	for time.Now().Before(deadline) {
		_ = ptmx.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, _ := ptmx.Read(buf)
		if n > 0 {
			raw = append(raw, buf[:n]...)
		}
		if strings.Contains(string(raw), "/help") {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatalf("menu never appeared: %q", raw)
	}
	if _, err := ptmx.Write([]byte("\x1b[B")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		got, _ := os.ReadFile(lineFile)
		if err != nil {
			t.Fatalf("child: %v line=%q", err, got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}
