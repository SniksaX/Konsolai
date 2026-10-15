package tools

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/siraj/konsolai/internal/safety"
)

// relPrefix maps a user path (relative, absolute, or empty) to a slash-separated
// prefix relative to the guard root. Empty or "." means the whole tree.
func relPrefix(g *safety.Guard, path string) (string, error) {
	if path == "" || path == "." {
		return "", nil
	}
	resolved, err := g.ResolvePath(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(g.Root(), resolved)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

func compileGlob(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	i := 0
	for i < len(glob) {
		if strings.HasPrefix(glob[i:], "**/") {
			b.WriteString("(?:.*/)?")
			i += 3
			continue
		}
		if strings.HasPrefix(glob[i:], "**") {
			b.WriteString(".*")
			i += 2
			continue
		}
		c := glob[i]
		switch c {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '[', ']', '{', '}', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
		i++
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}

func matchGlob(re *regexp.Regexp, glob, path string) bool {
	if !strings.Contains(glob, "/") {
		return re.MatchString(filepath.Base(path))
	}
	return re.MatchString(path)
}

func hasPathPrefix(file, prefix string) bool {
	if prefix == "" || prefix == "." {
		return true
	}
	prefix = strings.TrimSuffix(prefix, "/")
	return file == prefix || strings.HasPrefix(file, prefix+"/")
}
