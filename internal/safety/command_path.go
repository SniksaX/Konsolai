package safety

import (
	"path/filepath"
	"strings"
	"unicode"
)

// CheckCommandPaths resolves path words in a mutating command against dir.
// dir is the shell working directory. It is the join base for relative paths
// and is not added as an allowed root: a path must still sit inside Root or
// an allowed root. Read-only segments are left alone.
func (g *Guard) CheckCommandPaths(cmd, dir string) error {
	if g == nil {
		return nil
	}
	if dir == "" {
		dir = g.base
	}
	for _, seg := range SplitSegments(cmd) {
		if IsReadOnlyCommand(seg) {
			continue
		}
		for _, w := range strings.Fields(seg) {
			for _, p := range wordPaths(w) {
				if _, err := g.resolve(p, dir); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func wordPaths(word string) []string {
	w := peelRedirect(word)
	if w == "" {
		return nil
	}
	if strings.HasPrefix(w, "--") {
		i := strings.IndexByte(w, '=')
		if i < 0 {
			return nil
		}
		w = w[i+1:]
	} else if strings.HasPrefix(w, "-") {
		return nil
	} else {
		w = peelAssign(w)
	}
	w = unquoteWord(strings.TrimSpace(w))
	if w == "" || !pathWord(w) || benignPath(w) {
		return nil
	}
	return []string{w}
}

func peelRedirect(w string) string {
	i := 0
	for i < len(w) && w[i] >= '0' && w[i] <= '9' {
		i++
	}
	if i < len(w) && w[i] == '>' {
		return strings.TrimLeft(w[i:], ">")
	}
	return w
}

func peelAssign(w string) string {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return w
	}
	name := w[:i]
	if name == "" || !identStart(rune(name[0])) {
		return w
	}
	for _, r := range name {
		if !identCont(r) {
			return w
		}
	}
	return w[i+1:]
}

func identStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func identCont(r rune) bool {
	return identStart(r) || unicode.IsDigit(r)
}

func unquoteWord(w string) string {
	if len(w) >= 2 {
		if (w[0] == '\'' && w[len(w)-1] == '\'') || (w[0] == '"' && w[len(w)-1] == '"') {
			return w[1 : len(w)-1]
		}
	}
	return w
}

func pathWord(w string) bool {
	if w == "" || strings.Contains(w, "://") {
		return false
	}
	if strings.ContainsAny(w, " \t\"'`$(){}[]|&;<>!*?") {
		return false
	}
	return true
}

func benignPath(p string) bool {
	switch filepath.Clean(p) {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/stdin", "/dev/tty", "/dev/zero":
		return true
	default:
		return false
	}
}
