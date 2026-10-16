package repl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/reeflective/readline"
	"github.com/siraj/konsolai/internal/commands"
	"github.com/siraj/konsolai/internal/repo"
)

func Completer(reg *commands.Registry, cwdFn func() string) func(line []rune, cursor int) readline.Completions {
	return func(line []rune, cursor int) readline.Completions {
		if cursor < 0 {
			cursor = 0
		}
		if cursor > len(line) {
			cursor = len(line)
		}
		s := string(line[:cursor])
		start := lastTokenStart(s)
		tok := s[start:]
		cwd := ""
		if cwdFn != nil {
			cwd = cwdFn()
		}
		switch {
		case strings.HasPrefix(tok, "/"):
			return completeSlash(reg, tok)
		case strings.HasPrefix(tok, "@"):
			return completeAt(cwd, tok)
		default:
			return readline.Completions{}
		}
	}
}

func lastTokenStart(s string) int {
	i := len(s)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if unicode.IsSpace(r) {
			return i
		}
		i -= size
	}
	return 0
}

func completeSlash(reg *commands.Registry, tok string) readline.Completions {
	rest := strings.TrimPrefix(tok, "/")
	parts := strings.Fields(rest)
	space := strings.HasSuffix(tok, " ") || strings.Contains(strings.TrimPrefix(tok, "/"), " ")
	if !space && !strings.Contains(rest, " ") {
		var names []string
		for _, c := range reg.All() {
			n := "/" + c.Name
			if strings.HasPrefix(n, tok) {
				names = append(names, n)
			}
		}
		return readline.CompleteValues(cap50(names)...)
	}
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[len(parts)-1]
	}
	var opts []string
	switch cmd {
	case "copy":
		opts = []string{"answer", "all", "code", "cmd"}
	case "yes", "trace":
		opts = []string{"on", "off"}
	}
	var out []string
	for _, o := range opts {
		if arg == "" || strings.HasPrefix(o, arg) {
			out = append(out, "/"+cmd+" "+o)
		}
	}
	return readline.CompleteValues(cap50(out)...)
}

func completeAt(cwd, tok string) readline.Completions {
	pref := strings.TrimPrefix(tok, "@")
	dir := cwd
	base := pref
	if i := strings.LastIndex(pref, "/"); i >= 0 {
		dir = filepath.Join(cwd, pref[:i])
		base = pref[i+1:]
	}
	var cands []string
	root := repo.Root(cwd)
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		files, err := repo.Files(context.Background(), root)
		if err == nil {
			relCwd, _ := filepath.Rel(root, cwd)
			for _, f := range files {
				rel := f
				if relCwd != "" && relCwd != "." {
					if !strings.HasPrefix(f, filepath.ToSlash(relCwd)+"/") {
						continue
					}
					rel = strings.TrimPrefix(f, filepath.ToSlash(relCwd)+"/")
				}
				if strings.HasPrefix(rel, pref) || (base != "" && strings.HasPrefix(filepath.Base(rel), base)) {
					cands = append(cands, "@"+rel)
				}
			}
		}
	}
	ents, err := os.ReadDir(dir)
	if err == nil {
		prefixDir := ""
		if i := strings.LastIndex(pref, "/"); i >= 0 {
			prefixDir = pref[:i+1]
		}
		for _, e := range ents {
			name := e.Name()
			if base != "" && !strings.HasPrefix(name, base) {
				continue
			}
			p := "@" + prefixDir + name
			if e.IsDir() {
				p += "/"
			}
			cands = append(cands, p)
		}
	}
	return readline.CompleteValues(cap50(unique(cands))...)
}

func cap50(s []string) []string {
	if len(s) > 50 {
		return s[:50]
	}
	return s
}

func unique(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
