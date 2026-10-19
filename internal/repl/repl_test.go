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

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
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
		Profile: config.Profile{Name: "default", LLM: config.LLM{Model: "m", Endpoint: "http://127.0.0.1:9/v1"}},
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

func TestReplBannerOffUsesMutedLine(t *testing.T) {
	var out, errb bytes.Buffer
	prof := config.Builtin()
	prof.UI.Banner = false
	prof.LLM.Model = "m"
	env := &commands.Env{Profile: prof, Out: &out, Err: &errb, Cwd: t.TempDir()}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Reader: &scriptReader{lines: []string{"/exit"}},
		Version: "v9", Model: "m", Out: &out, Err: &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "konsolai v9 · m · /help for commands") {
		t.Fatalf("want muted line, got %q", out.String())
	}
	if strings.Contains(out.String(), "█") {
		t.Fatalf("pixel banner on non-TTY: %q", out.String())
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

func TestGitBranchUpdatesBetweenRenders(t *testing.T) {
	dir := t.TempDir()
	if err := osMkdirGit(dir, "ref: refs/heads/prod\n"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLUMNS", "80")
	a := formatFooter(footerInfo{Cwd: dir, Model: "m", Routing: "main"})
	if !strings.Contains(a, "prod") || strings.Contains(a, "other") {
		t.Fatalf("first %q", a)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/other\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := formatFooter(footerInfo{Cwd: dir, Model: "m", Routing: "main"})
	if !strings.Contains(b, "other") || strings.Contains(b, "prod") {
		t.Fatalf("second %q", b)
	}
}

func TestGitBranchWorktreeFile(t *testing.T) {
	root := t.TempDir()
	gitdir := filepath.Join(root, "real.git")
	if err := os.MkdirAll(gitdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if gitBranch(wt) != "work" {
		t.Fatalf("%q", gitBranch(wt))
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
		Cwd: dir, Model: "org/main-model", Routing: "fast",
		CtxPct: 12, ShowCtx: true, Down: true,
	})
	if !strings.Contains(s, "dev") || !strings.Contains(s, "main-model") || !strings.Contains(s, "ctx 12%") {
		t.Fatalf("left %q", s)
	}
	if strings.Contains(s, "▰") || strings.Contains(s, "▱") {
		t.Fatalf("meter cells at width 80: %q", s)
	}
	if !strings.Contains(s, "fast") || !strings.Contains(s, "●") {
		t.Fatalf("right %q", s)
	}
	sh := formatFooter(footerInfo{Cwd: dir, Model: "main-model", Routing: "main", Shell: true})
	if !strings.Contains(sh, "shell") || strings.Contains(sh, "main-model") {
		t.Fatalf("shell footer %q", sh)
	}
}

func TestContextMeter(t *testing.T) {
	if theme.DisplayWidth("▰") != 1 || theme.DisplayWidth("▱") != 1 {
		t.Fatalf("glyph width %d %d", theme.DisplayWidth("▰"), theme.DisplayWidth("▱"))
	}
	if ctxFilled(0) != 0 || ctxFilled(6) != 0 || ctxFilled(7) != 1 || ctxFilled(12) != 1 {
		t.Fatalf("low %d %d %d %d", ctxFilled(0), ctxFilled(6), ctxFilled(7), ctxFilled(12))
	}
	if ctxFilled(60) != 5 || ctxFilled(85) != 7 || ctxFilled(99) != 8 || ctxFilled(100) != 8 {
		t.Fatalf("high %d %d %d %d", ctxFilled(60), ctxFilled(85), ctxFilled(99), ctxFilled(100))
	}
	dir := t.TempDir()
	t.Setenv("COLUMNS", "120")
	s := formatFooter(footerInfo{Cwd: dir, Model: "m", Routing: "main", CtxPct: 12, ShowCtx: true})
	if strings.Count(s, "▰") != 1 || strings.Count(s, "▱") != 7 || !strings.Contains(s, "12%") {
		t.Fatalf("12%% meter %q", s)
	}
	hot := formatFooter(footerInfo{Cwd: dir, Model: "m", Routing: "main", CtxPct: 85, ShowCtx: true})
	if strings.Count(hot, "▰") != 7 || strings.Count(hot, "▱") != 1 || !strings.Contains(hot, "85%") {
		t.Fatalf("85%% meter %q", hot)
	}
	restore := theme.Force(true, true, "glacier", theme.Dark)
	defer restore()
	colored := ctxMeter(12, 120)
	if !strings.Contains(colored, "38;2;76;201;240") || !strings.Contains(colored, "▰") {
		t.Fatalf("accent cells %q", colored)
	}
	warn := ctxMeter(60, 120)
	if !strings.Contains(warn, "38;2;255;209;102") {
		t.Fatalf("warn cells %q", warn)
	}
	bad := ctxMeter(85, 120)
	if !strings.Contains(bad, "38;2;255;107;107") {
		t.Fatalf("error cells %q", bad)
	}
	narrow := ctxMeter(12, 80)
	if strings.Contains(narrow, "▰") || !strings.Contains(narrow, "ctx 12%") {
		t.Fatalf("narrow %q", narrow)
	}
}

func TestEchoPrimaryBoldNotLink(t *testing.T) {
	restore := theme.Force(true, true, "glacier", theme.Dark)
	defer restore()
	var b bytes.Buffer
	echoInput(&b, "/theme")
	s := b.String()
	mark := "\x1b[1;39m/theme\x1b[0m"
	idx := strings.Index(s, mark)
	if idx < 0 {
		t.Fatalf("body %q", s)
	}
	if !strings.Contains(s[:idx], "38;2;76;201;240") {
		t.Fatalf("chevron should be accent %q", s)
	}
	if strings.Contains(s[idx:], "38;2;") || strings.Contains(s[idx:], "4;") {
		t.Fatalf("echo text colored %q", s)
	}
}

func TestClearDrawsCompactHeader(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{
		Profile: config.Profile{Name: "p", LLM: config.LLM{Model: "m", Endpoint: "http://127.0.0.1:9/v1"}},
		Out:     &out, Err: &errb, Cwd: t.TempDir(), Version: "vtest",
	}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Reader: &scriptReader{lines: []string{"/clear", "/exit"}},
		Version: "vtest", Model: "m", Out: &out, Err: &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "KONSOLAI") {
		t.Fatalf("missing compact header %q", out.String())
	}
	if strings.Contains(out.String(), "╭") {
		t.Fatalf("full welcome on /clear %q", out.String())
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
	t.Setenv("HOME", "/users/u")
	if shortPath("/users/u/src/k") != "~/src/k" {
		t.Fatalf("%q", shortPath("/users/u/src/k"))
	}
	if shortPath("/users/u") != "~" {
		t.Fatalf("%q", shortPath("/users/u"))
	}
	if shortModel("acme/main-model") != "main-model" {
		t.Fatalf("%q", shortModel("acme/main-model"))
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
	if _, err := h.Write("token " + githubToken()); err != nil {
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
		Profile: config.Profile{Name: "p", LLM: config.LLM{Model: "m", Endpoint: "http://127.0.0.1:9/v1"}},
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

func TestSubmittedInputNotEchoed(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{
		Profile: config.Profile{Name: "p", LLM: config.LLM{Model: "m", Endpoint: "http://127.0.0.1:9/v1"}},
		Out:     &out,
		Err:     &errb,
		Cwd:     t.TempDir(),
		Version: "test",
	}
	unique := "unique-scrollback-echo-xyz"
	code := Run(context.Background(), Config{
		Runner:  &turn.Runner{Profile: env.Profile, Out: &out, Err: &errb},
		Env:     env,
		Reg:     commands.Default(),
		Reader:  &scriptReader{lines: []string{unique, "/exit"}},
		Version: "test",
		Model:   "m",
		Out:     &out,
		Err:     &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if strings.Count(out.String(), unique) != 1 || !strings.Contains(out.String(), "  › "+unique) {
		t.Fatalf("want one echo line, got %q", out.String())
	}
}

type promptCountReader struct {
	prompts []string
	lines   []string
	i       int
}

func (p *promptCountReader) ReadLine(_ context.Context, prompt string) (string, error) {
	p.prompts = append(p.prompts, prompt)
	if p.i >= len(p.lines) {
		return "", ErrEOF
	}
	line := p.lines[p.i]
	p.i++
	return line, nil
}

func (p *promptCountReader) Close() error { return nil }

func TestNoEmptyPromptInScrollback(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{Out: &out, Err: &errb, Cwd: t.TempDir(), Profile: config.Profile{Name: "p"}}
	n := 0
	rd := &fnReader{fn: func() (string, error) {
		n++
		switch n {
		case 1:
			return "", ErrInterrupt
		case 2:
			return "/exit", nil
		default:
			return "", ErrEOF
		}
	}}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Reader: rd, Out: &out, Err: &errb, Version: "v",
	})
	if code != 0 {
		t.Fatalf("%d", code)
	}
	if strings.Contains(out.String(), "  › \n") {
		t.Fatalf("empty prompt leaked into scrollback: %q", out.String())
	}
	if n != 2 {
		t.Fatalf("prompt renders %d want 2", n)
	}
}

func TestPromptRenderCount(t *testing.T) {
	var out, errb bytes.Buffer
	env := &commands.Env{Out: &out, Err: &errb, Cwd: t.TempDir(), Profile: config.Profile{Name: "p"}}
	rd := &promptCountReader{lines: []string{"/help", "/exit"}}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Reader: rd, Out: &out, Err: &errb, Version: "v",
	})
	if code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if len(rd.prompts) != 2 {
		t.Fatalf("prompt renders %d want 2: %q", len(rd.prompts), rd.prompts)
	}
	if strings.Count(out.String(), "  › /help") != 1 || strings.Count(out.String(), "  › /exit") != 1 {
		t.Fatalf("want one echo per accepted line: %q", out.String())
	}
}

func TestErrs(t *testing.T) {
	if !errors.Is(ErrInterrupt, ErrInterrupt) || !errors.Is(ErrEOF, ErrEOF) {
		t.Fatal("sentinel")
	}
}
