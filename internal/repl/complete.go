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

const (
	slashNavHint = "↑/↓ navigate · tab complete · enter select · esc cancel"
	slashStyle   = "38;5;141"
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
		if strings.HasPrefix(s, "/") {
			return completeSlash(reg, s)
		}
		start := lastTokenStart(s)
		tok := s[start:]
		cwd := ""
		if cwdFn != nil {
			cwd = cwdFn()
		}
		if strings.HasPrefix(tok, "@") {
			return completeAt(cwd, tok)
		}
		return readline.Completions{}
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

func completeSlash(reg *commands.Registry, line string) readline.Completions {
	items := slashItems(reg, line)
	if len(items) == 0 {
		return readline.Completions{}
	}
	vals := make([]string, 0, len(items)*3)
	for _, it := range items {
		vals = append(vals, it.Value, it.Desc, slashStyle)
	}
	comps := readline.CompleteStyledValuesDescribed(vals...)
	comps.PREFIX = slashPrefix(line)
	return comps.DisplayList().Usage(slashNavHint).NoSort()
}

type slashItem struct {
	Value string
	Desc  string
}

func slashItems(reg *commands.Registry, line string) []slashItem {
	if !strings.HasPrefix(line, "/") || reg == nil {
		return nil
	}
	rest := strings.TrimPrefix(line, "/")
	parts := strings.Fields(rest)
	space := strings.HasSuffix(line, " ")
	if !space && len(parts) <= 1 {
		var out []slashItem
		for _, c := range reg.All() {
			n := "/" + c.Name
			if strings.HasPrefix(n, line) {
				out = append(out, slashItem{Value: n, Desc: c.Summary})
			}
		}
		return cap50items(out)
	}
	if len(parts) == 0 {
		return nil
	}
	cmd := parts[0]
	arg := ""
	if !space && len(parts) > 1 {
		arg = parts[len(parts)-1]
	}
	var out []slashItem
	for _, o := range slashArgs(cmd) {
		if arg == "" || strings.HasPrefix(o.Value, arg) {
			out = append(out, o)
		}
	}
	return cap50items(out)
}

func slashPrefix(line string) string {
	if !strings.HasPrefix(line, "/") {
		return line
	}
	rest := strings.TrimPrefix(line, "/")
	if !strings.Contains(rest, " ") && !strings.HasSuffix(line, " ") {
		return line
	}
	if strings.HasSuffix(line, " ") {
		return ""
	}
	i := strings.LastIndex(line, " ")
	if i < 0 {
		return line
	}
	return line[i+1:]
}

func slashArgs(cmd string) []slashItem {
	switch cmd {
	case "copy":
		return []slashItem{
			{Value: "answer", Desc: "Copy the last answer"},
			{Value: "all", Desc: "Copy the last prompt and answer"},
			{Value: "code", Desc: "Copy a code block from the last answer"},
			{Value: "cmd", Desc: "Copy a command from the last answer"},
		}
	case "think":
		return []slashItem{
			{Value: "on", Desc: "Always think"},
			{Value: "off", Desc: "Never think"},
			{Value: "auto", Desc: "Think when the intent asks for it"},
		}
	case "yes":
		return []slashItem{
			{Value: "on", Desc: "Auto-approve for this session"},
			{Value: "off", Desc: "Ask before mutating actions"},
		}
	case "trace":
		return []slashItem{
			{Value: "on", Desc: "Print per-call traces"},
			{Value: "off", Desc: "Hide per-call traces"},
		}
	default:
		return nil
	}
}

func cap50items(s []slashItem) []slashItem {
	if len(s) > 50 {
		return s[:50]
	}
	return s
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
