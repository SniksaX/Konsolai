package repl

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
)

func gitBranch(cwd string) string {
	head := readGitHEAD(cwd)
	if head == "" {
		return ""
	}
	const pref = "ref: refs/heads/"
	if strings.HasPrefix(head, pref) {
		return strings.TrimPrefix(head, pref)
	}
	if len(head) > 7 {
		return head[:7]
	}
	return head
}

func readGitHEAD(cwd string) string {
	dir := cwd
	if dir == "" {
		return ""
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		fi, err := os.Lstat(gitPath)
		if err == nil {
			headPath := filepath.Join(gitPath, "HEAD")
			if !fi.IsDir() {
				b, err := os.ReadFile(gitPath)
				if err != nil {
					return ""
				}
				gitdir := parseGitdir(string(b), dir)
				if gitdir == "" {
					return ""
				}
				headPath = filepath.Join(gitdir, "HEAD")
			}
			b, err := os.ReadFile(headPath)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(b))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func parseGitdir(contents, base string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		const p = "gitdir:"
		if strings.HasPrefix(strings.ToLower(line), p) {
			dir := strings.TrimSpace(line[len(p):])
			if dir == "" {
				return ""
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(base, dir)
			}
			return filepath.Clean(dir)
		}
	}
	return ""
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

var (
	dirtyMu  sync.Mutex
	dirtyAt  time.Time
	dirtyOn  bool
	dirtyCwd string
)

func gitDirty(cwd string) bool {
	if cwd == "" {
		return false
	}
	dirtyMu.Lock()
	defer dirtyMu.Unlock()
	if cwd == dirtyCwd && time.Since(dirtyAt) < 5*time.Second {
		return dirtyOn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain")
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return dirtyOn
	}
	dirtyCwd = cwd
	dirtyAt = time.Now()
	dirtyOn = err == nil && len(bytes.TrimSpace(out)) > 0
	return dirtyOn
}

func formatFooter(f footerInfo) string {
	width := theme.TermWidth()
	if width < 20 {
		width = 20
	}
	sep := theme.Style(theme.Subtle, "·")
	join := func(parts []string) string {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, " "+sep+" ")
	}

	path := theme.Style(theme.Muted, shortPath(f.Cwd))
	br := gitBranch(f.Cwd)
	branch := ""
	if br != "" {
		if gitDirty(f.Cwd) {
			br += "*"
		}
		branch = theme.Style(theme.Accent, br)
	}
	modelWord := shortModel(f.Model)
	if f.Shell {
		modelWord = "shell"
	}
	model := theme.Style(theme.Muted, modelWord)
	ctx := ""
	if f.ShowCtx {
		label := "ctx " + itoa(clampPct(f.CtxPct)) + "%"
		switch {
		case f.CtxPct >= 85:
			ctx = theme.Style(theme.Error, label)
		case f.CtxPct >= 60:
			ctx = theme.Style(theme.Warn, label)
		default:
			ctx = theme.Style(theme.Muted, label)
		}
	}
	routing := f.Routing
	if routing == "" {
		routing = "main"
	}
	right := theme.Style(theme.Muted, routing)
	if f.Down {
		right = theme.Style(theme.Error, "●") + " " + right
	}

	dropModel := width < 60
	dropCtx := false
	build := func() string {
		left := join([]string{path, branch, pick(dropModel, "", model)})
		rightS := join([]string{pick(dropCtx, "", ctx), right})
		pad := width - 2 - theme.VisibleWidth(left) - theme.VisibleWidth(rightS)
		if pad < 1 {
			return ""
		}
		return left + strings.Repeat(" ", pad) + rightS
	}
	out := build()
	if out == "" && !dropModel {
		dropModel = true
		out = build()
	}
	if out == "" && f.ShowCtx && !dropCtx {
		dropCtx = true
		out = build()
	}
	if out == "" {
		left := join([]string{path, branch})
		out = theme.TruncateChrome(left+" "+sep+" "+right, width)
	}
	if theme.VisibleWidth(out) > width {
		out = theme.TruncateChrome(out, width)
	}
	return out
}

func pick(skip bool, empty, val string) string {
	if skip {
		return empty
	}
	return val
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
