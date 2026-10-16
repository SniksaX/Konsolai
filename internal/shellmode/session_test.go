package shellmode

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
)

func evalPath(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return got
}

func TestCdVariantsAndGuard(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	wantRoot := evalPath(t, root)
	wantSub := evalPath(t, sub)
	g, err := safety.NewAt(root, root)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	s := New(root)
	s.Guard = g
	s.Stdout = &out
	s.Stderr = &errb
	s.Run = func(context.Context, string, string, *os.File, io.Writer) (int, time.Duration, string, error) {
		t.Fatal("unexpected run")
		return 0, 0, "", nil
	}

	if _, err := s.Handle(context.Background(), "cd sub"); err != nil {
		t.Fatal(err)
	}
	if s.Cwd != wantSub {
		t.Fatalf("cd sub cwd %q want %q", s.Cwd, wantSub)
	}
	if g.Base() != wantSub {
		t.Fatalf("guard base %q want %q", g.Base(), wantSub)
	}

	out.Reset()
	if _, err := s.Handle(context.Background(), "pwd"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != wantSub {
		t.Fatalf("pwd %q", out.String())
	}

	if _, err := s.Handle(context.Background(), "cd -"); err != nil {
		t.Fatal(err)
	}
	if s.Cwd != wantRoot {
		t.Fatalf("cd - %q", s.Cwd)
	}

	if _, err := s.Handle(context.Background(), "cd sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Handle(context.Background(), "cd ~"); err != nil {
		t.Fatal(err)
	}
	if s.Cwd != wantRoot {
		t.Fatalf("cd ~ %q", s.Cwd)
	}

	if _, err := s.Handle(context.Background(), "cd "+wantSub); err != nil {
		t.Fatal(err)
	}
	if s.Cwd != wantSub {
		t.Fatalf("cd abs %q", s.Cwd)
	}

	toAI, err := s.Handle(context.Background(), "exit")
	if err != nil || !toAI {
		t.Fatalf("exit toAI=%v err=%v", toAI, err)
	}
}

func TestHandleRecordsHistory(t *testing.T) {
	var out bytes.Buffer
	s := New(t.TempDir())
	s.Stdout = &out
	s.Run = func(_ context.Context, cwd, line string, _ *os.File, w io.Writer) (int, time.Duration, string, error) {
		_, _ = w.Write([]byte("hello\n"))
		return 0, 10 * time.Millisecond, "hello\n", nil
	}
	toAI, err := s.Handle(context.Background(), "echo hello")
	if err != nil || toAI {
		t.Fatalf("toAI=%v err=%v", toAI, err)
	}
	if s.Hist.Len() != 1 || s.Hist.Items()[0].Cmd != "echo hello" {
		t.Fatalf("%+v", s.Hist.Items())
	}
	if !strings.Contains(s.Hist.Format(), "<shell_history>") {
		t.Fatalf("%q", s.Hist.Format())
	}
}
