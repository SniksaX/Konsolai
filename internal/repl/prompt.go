package repl

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/ui"
)

func gitBranch(cwd string) string {
	root := repo.Root(cwd)
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	const pref = "ref: refs/heads/"
	if strings.HasPrefix(s, pref) {
		return strings.TrimPrefix(s, pref)
	}
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func shortPath(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if cwd == home {
			return "~"
		}
		if strings.HasPrefix(cwd, home+string(filepath.Separator)) {
			return "~" + cwd[len(home):]
		}
	}
	return cwd
}

func shortModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "-"
	}
	if i := strings.LastIndex(model, "/"); i >= 0 && i+1 < len(model) {
		model = model[i+1:]
	}
	return model
}

func inputPrompt(shell bool) string {
	p := ui.Current()
	if shell {
		return p.Warn("$ ")
	}
	return p.Accent("› ")
}

type footerInfo struct {
	Cwd     string
	Model   string
	Routing string
	CtxPct  int
	ShowCtx bool
	Down    bool
	Shell   bool
}

func formatFooter(f footerInfo) string {
	p := ui.Current()
	var left []string
	if sp := shortPath(f.Cwd); sp != "" {
		left = append(left, sp)
	}
	if br := gitBranch(f.Cwd); br != "" {
		left = append(left, br)
	}
	if f.Shell {
		left = append(left, "shell")
	} else {
		left = append(left, shortModel(f.Model))
	}
	if f.ShowCtx {
		left = append(left, "ctx "+itoa(clampPct(f.CtxPct))+"%")
	}
	leftS := strings.Join(left, " · ")
	routing := f.Routing
	if routing == "" {
		routing = "main"
	}
	rightS := "routing " + routing
	leftPaint := p.Muted(leftS)
	rightPaint := p.Muted(rightS)
	if f.Down {
		rightPaint = p.Error("●") + " " + rightPaint
	}
	width := termCols()
	pad := width - visLen(leftPaint) - visLen(rightPaint)
	if pad < 1 {
		pad = 1
	}
	return leftPaint + strings.Repeat(" ", pad) + rightPaint
}

func clampPct(n int) int {
	if n < 0 {
		return 0
	}
	if n > 99 {
		return 99
	}
	return n
}

func visLen(s string) int {
	n := 0
	skip := false
	for _, r := range s {
		if r == '\033' {
			skip = true
			continue
		}
		if skip {
			if r == 'm' {
				skip = false
			}
			continue
		}
		n++
	}
	return n
}

func termCols() int {
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stderr.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno == 0 && ws.Col > 0 {
		return int(ws.Col)
	}
	return 80
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
