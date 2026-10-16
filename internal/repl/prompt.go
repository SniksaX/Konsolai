package repl

import (
	"os"
	"path/filepath"
	"strings"

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

func folderName(cwd string) string {
	base := filepath.Base(cwd)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return cwd
	}
	return base
}

func formatPrompt(cwd string, ctxPct int, showCtx, down bool) string {
	p := ui.Current()
	var b strings.Builder
	if down {
		b.WriteString(p.Error("●") + " ")
	}
	b.WriteString(folderName(cwd))
	if br := gitBranch(cwd); br != "" {
		b.WriteString(" (" + br + ")")
	}
	if showCtx {
		b.WriteString(p.Muted(sprintfCtx(ctxPct)))
	}
	b.WriteString(" › ")
	return b.String()
}

func sprintfCtx(p int) string {
	if p < 0 {
		p = 0
	}
	if p > 99 {
		p = 99
	}
	return " · ctx " + itoa(p) + "%"
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
