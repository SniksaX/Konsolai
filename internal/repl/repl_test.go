package repl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/commands"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/turn"
)

type scriptReader struct {
	lines []string
	i     int
}

func (s *scriptReader) ReadLine(context.Context, string) (string, error) {
	if s.i >= len(s.lines) {
		return "", ErrEOF
	}
	line := s.lines[s.i]
	s.i++
	return line, nil
}

func (s *scriptReader) Close() error { return nil }

func TestReplCommandsAndExit(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{
		Profile: config.Profile{Name: "default", Model: "m", Endpoint: "http://127.0.0.1:9/v1"},
		Out:     &out,
		Err:     &errb,
		Cwd:     t.TempDir(),
		Version: "test",
	}
	code := Run(context.Background(), Config{
		Runner:  &turn.Runner{Profile: env.Profile, Out: &out, Err: &errb},
		Env:     env,
		Reg:     commands.Default(),
		Reader:  &scriptReader{lines: []string{"/help", "/exit"}},
		Version: "test",
		Model:   "m",
		Out:     &out,
		Err:     &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "konsolai test · m · /help") {
		t.Fatalf("banner %q", out.String())
	}
	if !strings.Contains(out.String(), "/help") {
		t.Fatalf("help %q", out.String())
	}
}

func TestReplInterruptEmpty(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{Out: &out, Err: &errb, Cwd: t.TempDir(), Profile: config.Profile{Name: "p"}}
	n := 0
	rd := &fnReader{fn: func() (string, error) {
		n++
		if n == 1 {
			return "", ErrInterrupt
		}
		return "", ErrEOF
	}}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Reader: rd, Out: &out, Err: &errb, Version: "v",
	})
	if code != 0 {
		t.Fatalf("%d", code)
	}
	if !strings.Contains(out.String(), "press Ctrl+D or type /exit to quit") {
		t.Fatalf("%q", out.String())
	}
}

type fnReader struct{ fn func() (string, error) }

func (f *fnReader) ReadLine(context.Context, string) (string, error) { return f.fn() }
func (f *fnReader) Close() error                                     { return nil }

func TestGitBranchFromHEAD(t *testing.T) {
	dir := t.TempDir()
	if err := osMkdirGit(dir, "ref: refs/heads/dev\n"); err != nil {
		t.Fatal(err)
	}
	if gitBranch(dir) != "dev" {
		t.Fatalf("%q", gitBranch(dir))
	}
}

func osMkdirGit(dir, head string) error {
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte(head), 0644)
}

func TestFormatFooter(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	dir := t.TempDir()
	if err := osMkdirGit(dir, "ref: refs/heads/dev\n"); err != nil {
		t.Fatal(err)
	}
	s := formatFooter(footerInfo{
		Cwd: dir, Model: "org/qwen38.nvfp4", Routing: "fast",
		CtxPct: 12, ShowCtx: true, Down: true,
	})
	if !strings.Contains(s, "dev") || !strings.Contains(s, "qwen38.nvfp4") || !strings.Contains(s, "ctx 12%") {
		t.Fatalf("left %q", s)
	}
	if !strings.Contains(s, "routing fast") || !strings.Contains(s, "●") {
		t.Fatalf("right %q", s)
	}
	sh := formatFooter(footerInfo{Cwd: dir, Model: "qwen", Routing: "main", Shell: true})
	if !strings.Contains(sh, "shell") || strings.Contains(sh, "qwen") {
		t.Fatalf("shell footer %q", sh)
	}
}

func TestInputPrompt(t *testing.T) {
	if !strings.Contains(inputPrompt(false), "› ") {
		t.Fatalf("ai %q", inputPrompt(false))
	}
	if !strings.Contains(inputPrompt(true), "$ ") {
		t.Fatalf("shell %q", inputPrompt(true))
	}
}

func TestShortPathAndModel(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	if shortPath("/home/u/src/k") != "~/src/k" {
		t.Fatalf("%q", shortPath("/home/u/src/k"))
	}
	if shortPath("/home/u") != "~" {
		t.Fatalf("%q", shortPath("/home/u"))
	}
	if shortModel("acme/qwen38.nvfp4") != "qwen38.nvfp4" {
		t.Fatalf("%q", shortModel("acme/qwen38.nvfp4"))
	}
}

func TestSplitCmd(t *testing.T) {
	n, a := splitCmd(`/resume 3 "and then?"`)
	if n != "resume" || len(a) != 2 || a[1] != "and then?" {
		t.Fatalf("%s %v", n, a)
	}
}

func TestHistoryWriteRedacts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	h := loadFileHistory(HistoryPath())
	if _, err := h.Write("token ghp_abcdefghijklmnopqrstuvwxyz012345"); err != nil {
		t.Fatal(err)
	}
	if h.Len() != 1 || strings.Contains(h.lines[0], "ghp_") {
		t.Fatalf("%v", h.lines)
	}
}

func TestCompleterSlashAndAt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	fn := Completer(commands.Default(), func() string { return dir })
	_ = fn([]rune("/yes o"), 6)
	_ = fn([]rune("/copy a"), 7)
	_ = fn([]rune("@ma"), 3)
	if lastTokenStart("foo @bar") != 4 {
		t.Fatalf("token start %d", lastTokenStart("foo @bar"))
	}
}

func TestReadLineSeesTrailingNewline(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{
		Profile: config.Profile{Name: "p", Model: "m", Endpoint: "http://127.0.0.1:9/v1"},
		Out:     &out,
		Err:     &errb,
		Cwd:     t.TempDir(),
		Version: "test",
	}
	rd := &checkNLReader{out: &out, lines: []string{"/help", "/exit"}}
	code := Run(context.Background(), Config{
		Runner:  &turn.Runner{Profile: env.Profile, Out: &out, Err: &errb},
		Env:     env,
		Reg:     commands.Default(),
		Reader:  rd,
		Version: "test",
		Model:   "m",
		Out:     &out,
		Err:     &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if rd.calls < 2 {
		t.Fatalf("calls %d", rd.calls)
	}
}

type checkNLReader struct {
	out   *bytes.Buffer
	lines []string
	i     int
	calls int
}

func (c *checkNLReader) ReadLine(context.Context, string) (string, error) {
	c.calls++
	if !strings.HasSuffix(c.out.String(), "\n") {
		return "", fmt.Errorf("output before ReadLine %d does not end with newline: %q", c.calls, c.out.String())
	}
	if c.i >= len(c.lines) {
		return "", ErrEOF
	}
	line := c.lines[c.i]
	c.i++
	return line, nil
}

func (c *checkNLReader) Close() error { return nil }

func TestShellCtrlDReturnsToAI(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{Out: &out, Err: &errb, Cwd: t.TempDir(), Profile: config.Profile{Name: "p"}}
	n := 0
	rd := &fnReader{fn: func() (string, error) {
		n++
		switch n {
		case 1:
			return "/cmd", nil
		case 2:
			return "", ErrEOF
		case 3:
			return "/exit", nil
		default:
			return "", ErrEOF
		}
	}}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Reader: rd, Out: &out, Err: &errb, Version: "v",
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if n < 3 {
		t.Fatalf("Ctrl+D quit the REPL instead of leaving shell mode; calls=%d", n)
	}
}

func TestErrs(t *testing.T) {
	if !errors.Is(ErrInterrupt, ErrInterrupt) || !errors.Is(ErrEOF, ErrEOF) {
		t.Fatal("sentinel")
	}
}
