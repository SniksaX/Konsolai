package repl

import (
	"bytes"
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/siraj/konsolai/internal/config"
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
	Cwd       string
	Model     string
	Routing   string
	CtxPct    int
	ShowCtx   bool
	Down      bool
	Shell     bool
	GitQuery  time.Duration
	CtxWarn   int
	CtxHot    int
	CtxCells  int
	CtxNarrow int
}

var (
	dirtyMu  sync.Mutex
	dirtyAt  time.Time
	dirtyOn  bool
	dirtyCwd string
)

func gitDirty(cwd string) bool {
	return gitDirtyFor(cwd, 0)
}

func gitDirtyFor(cwd string, timeout time.Duration) bool {
	if cwd == "" {
		return false
	}
	if timeout <= 0 {
		timeout = config.Builtin().GitQueryTimeout()
	}
	dirtyMu.Lock()
	defer dirtyMu.Unlock()
	if cwd == dirtyCwd && time.Since(dirtyAt) < 5*time.Second {
		return dirtyOn
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
		if gitDirtyFor(f.Cwd, f.GitQuery) {
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
		ctx = paintMeter(f.CtxPct, width, meterStyle{
			Warn: f.CtxWarn, Hot: f.CtxHot, Cells: f.CtxCells, Narrow: f.CtxNarrow,
		})
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

type meterStyle struct {
	Warn   int
	Hot    int
	Cells  int
	Narrow int
}

func (m meterStyle) norm() meterStyle {
	if m.Warn <= 0 {
		m.Warn = 60
	}
	if m.Hot <= 0 {
		m.Hot = 85
	}
	if m.Cells <= 0 {
		m.Cells = 8
	}
	if m.Narrow <= 0 {
		m.Narrow = 90
	}
	return m
}

func ctxMeter(pct, width int) string {
	return paintMeter(pct, width, meterStyle{})
}

func paintMeter(pct, width int, m meterStyle) string {
	m = m.norm()
	shown := clampPct(pct)
	if width < m.Narrow {
		label := "ctx " + itoa(shown) + "%"
		switch {
		case pct >= m.Hot:
			return theme.Style(theme.Error, label)
		case pct >= m.Warn:
			return theme.Style(theme.Warn, label)
		default:
			return theme.Style(theme.Muted, label)
		}
	}
	filled := filledCells(shown, m.Cells)
	role := theme.Accent
	switch {
	case pct >= m.Hot:
		role = theme.Error
	case pct >= m.Warn:
		role = theme.Warn
	}
	cells := theme.Style(role, strings.Repeat("▰", filled)) +
		theme.Style(theme.Subtle, strings.Repeat("▱", m.Cells-filled))
	return theme.Style(theme.Muted, "ctx ") + cells + theme.Style(theme.Muted, " "+itoa(shown)+"%")
}

func ctxFilled(pct int) int {
	return filledCells(pct, 8)
}

func filledCells(pct, cells int) int {
	if cells < 1 {
		cells = 8
	}
	if pct < 0 {
		pct = 0
	}
	n := int(math.Round(float64(pct) / (100 / float64(cells))))
	if n < 0 {
		return 0
	}
	if n > cells {
		return cells
	}
	return n
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
