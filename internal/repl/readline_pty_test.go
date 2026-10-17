package repl

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func TestReadlineSubmittedOnceOnPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") != "1" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "1" {
		rd, err := NewReadline(nil, func() string { return "cwd · branch" }, nil)
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
