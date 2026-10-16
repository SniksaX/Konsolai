package shellmode

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/reeflective/readline"
)

const compgenTimeout = 300 * time.Millisecond

type ExecFunc func(ctx context.Context, cwd, script string) (string, error)

func DefaultExec(ctx context.Context, cwd, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, compgenTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = cwd
	out, err := cmd.Output()
	return string(out), err
}

var builtins = []string{"cd", "pwd", "exit"}

func (s *Session) Complete(line string, cursor int) readline.Completions {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}
	exec := s.Exec
	if exec == nil {
		exec = DefaultExec
	}
	return completeLine(line[:cursor], s.Cwd, exec)
}

func completeLine(prefixLine, cwd string, exec ExecFunc) readline.Completions {
	word, isFirst, afterCd := completeWord(prefixLine)
	script := "compgen -f -- " + bashQuote(word)
	switch {
	case isFirst:
		script = "compgen -c -- " + bashQuote(word)
	case afterCd:
		script = "compgen -d -- " + bashQuote(word)
	}
	out, _ := exec(context.Background(), cwd, script)
	names := ParseCompgen(out)
	if isFirst {
		names = append(names, matchBuiltins(word)...)
		names = uniqueSorted(names)
	}
	comps := readline.CompleteValues(names...)
	comps.PREFIX = word
	return comps
}

func completeWord(s string) (word string, first, afterCd bool) {
	start := lastTokenStart(s)
	word = s[start:]
	rest := strings.TrimSpace(s[:start])
	first = rest == ""
	if !first {
		fields := strings.Fields(rest)
		afterCd = len(fields) == 1 && fields[0] == "cd"
	}
	return word, first, afterCd
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

func matchBuiltins(prefix string) []string {
	var out []string
	for _, b := range builtins {
		if prefix == "" || strings.HasPrefix(b, prefix) {
			out = append(out, b)
		}
	}
	return out
}

func ParseCompgen(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

func bashQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func uniqueSorted(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
