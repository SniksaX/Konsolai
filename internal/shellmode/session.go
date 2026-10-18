package shellmode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

type RunFunc func(ctx context.Context, cwd, line string, stdin *os.File, stdout io.Writer) (exit int, dur time.Duration, capture string, err error)

type Session struct {
	Cwd     string
	OldPwd  string
	Guard   *safety.Guard
	Hist    History
	Stdin   *os.File
	Stdout  io.Writer
	Stderr  io.Writer
	Run     RunFunc
	Exec    ExecFunc
	OnClear func()
}

func New(cwd string) *Session {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &Session{
		Cwd:    cwd,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Run:    RunPty,
		Exec:   DefaultExec,
	}
}

func (s *Session) out() io.Writer {
	if s != nil && s.Stdout != nil {
		return s.Stdout
	}
	return os.Stdout
}

func (s *Session) errw() io.Writer {
	if s != nil && s.Stderr != nil {
		return s.Stderr
	}
	return os.Stderr
}

// Handle runs a shell-mode line. toAI is true for exit / /cmd.
func (s *Session) Handle(ctx context.Context, line string) (toAI bool, err error) {
	line = strings.TrimRight(line, "\n")
	trim := strings.TrimSpace(line)
	if trim == "" {
		return false, nil
	}
	if trim == "exit" || trim == "/cmd" {
		return true, nil
	}
	name, arg, builtin := parseBuiltin(line)
	switch {
	case builtin && name == "cd":
		if err := s.Cd(arg); err != nil {
			fmt.Fprintln(s.errw(), "konsolai:", err)
			return false, err
		}
		return false, nil
	case builtin && name == "pwd":
		fmt.Fprintln(s.out(), s.Cwd)
		return false, nil
	case builtin && name == "exit":
		return true, nil
	case builtin && (name == "clear" || name == "reset"):
		fmt.Fprint(s.out(), theme.ClearScroll)
		if s.OnClear != nil {
			s.OnClear()
		}
		return false, nil
	}
	run := s.Run
	if run == nil {
		run = RunPty
	}
	code, dur, cap, err := run(ctx, s.Cwd, line, s.Stdin, s.out())
	s.Hist.Add(line, cap)
	if code != 0 || dur > 2*time.Second {
		fmt.Fprintln(s.out(), ui.Current().Muted(fmt.Sprintf("exit %d · %.1fs", code, dur.Seconds())))
	}
	return false, err
}

func (s *Session) Cd(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" || dir == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("shellmode: cd: %w", err)
		}
		dir = home
	} else if dir == "-" {
		if s.OldPwd == "" {
			return fmt.Errorf("shellmode: cd: OLDPWD not set")
		}
		dir = s.OldPwd
	} else if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("shellmode: cd: %w", err)
		}
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.Cwd, dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("shellmode: cd: %w", err)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("shellmode: cd: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("shellmode: cd: not a directory")
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	s.OldPwd = s.Cwd
	s.Cwd = abs
	if s.Guard != nil {
		if err := s.Guard.SetBase(abs); err != nil {
			if g, err2 := safety.NewAt(repo.Root(abs), abs); err2 == nil {
				s.Guard = g
			}
		}
	}
	return nil
}

func parseBuiltin(line string) (name, arg string, ok bool) {
	if strings.ContainsAny(line, ";|&") {
		return "", "", false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", "", false
	}
	switch fields[0] {
	case "cd":
		arg := strings.TrimSpace(trimFirstWord(line))
		arg = unquote(arg)
		return "cd", arg, true
	case "pwd":
		if len(fields) == 1 {
			return "pwd", "", true
		}
	case "exit":
		if len(fields) == 1 {
			return "exit", "", true
		}
	case "clear", "reset":
		if len(fields) == 1 {
			return fields[0], "", true
		}
	}
	return "", "", false
}

func trimFirstWord(line string) string {
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	i := strings.IndexFunc(line, unicode.IsSpace)
	if i < 0 {
		return ""
	}
	return line[i:]
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
