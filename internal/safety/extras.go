package safety

import (
	"os"
	"path/filepath"
	"strings"
)

var deniedHomeSubs = []string{
	".ssh",
	".gnupg",
	".config",
	filepath.Join(".local", "share", "keyrings"),
}

func (g *Guard) AllowRoots(roots []string) {
	for _, r := range roots {
		if r == "" {
			continue
		}
		g.extra = append(g.extra, r)
	}
}

func (g *Guard) ExtraRoots() []string { return g.extra }

func (g *Guard) allowedAbs(abs string) bool {
	if containsPath(g.root, abs) {
		return true
	}
	for _, e := range g.extra {
		if containsPath(e, abs) {
			return true
		}
	}
	return false
}

func expandHome(p, home string) (string, bool) {
	if !strings.HasPrefix(p, "~") {
		return p, false
	}
	if home == "" {
		return "", false
	}
	rest := p[1:]
	if rest == "" {
		return home, true
	}
	if !strings.HasPrefix(rest, "/") {
		return "", false
	}
	return filepath.Join(home, rest[1:]), true
}

func hasDotSegment(p string) bool {
	p = strings.TrimPrefix(p, "~")
	if strings.HasPrefix(p, "/") {
		p = p[1:]
	}
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return true
		}
	}
	return false
}

func underDeniedHome(abs, home string) bool {
	for _, sub := range deniedHomeSubs {
		deny := filepath.Join(home, sub)
		if containsPath(deny, abs) {
			return true
		}
	}
	return false
}

// NamedRoots returns extra allowed roots named as absolute or ~ paths in request.
func NamedRoots(request, home string) []string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home == "" {
		return nil
	}
	home = filepath.Clean(home)
	seen := map[string]bool{}
	var out []string
	for _, tok := range pathTokens(request) {
		abs, ok := acceptNamed(tok, home)
		if !ok || seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

func acceptNamed(tok, home string) (string, bool) {
	tok = strings.TrimSpace(tok)
	if tok == "" || hasDotSegment(tok) {
		return "", false
	}
	var abs string
	if strings.HasPrefix(tok, "~") {
		exp, ok := expandHome(tok, home)
		if !ok {
			return "", false
		}
		abs = filepath.Clean(exp)
	} else if filepath.IsAbs(tok) {
		abs = filepath.Clean(tok)
	} else {
		return "", false
	}
	if abs == home || !containsPath(home, abs) {
		return "", false
	}
	if underDeniedHome(abs, home) {
		return "", false
	}
	if _, err := os.Lstat(abs); err == nil {
		return abs, true
	}
	parent, err := nearestExisting(abs)
	if err != nil {
		return "", false
	}
	if !containsPath(home, parent) {
		return "", false
	}
	return abs, true
}

func nearestExisting(p string) (string, error) {
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			return cur, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", os.ErrNotExist
		}
		cur = parent
	}
}

func pathTokens(s string) []string {
	var out []string
	var b strings.Builder
	var quote byte
	flush := func() {
		t := strings.Trim(b.String(), " \t")
		b.Reset()
		if t != "" {
			out = append(out, t)
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				flush()
				continue
			}
			b.WriteByte(c)
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == ',' || c == ';' {
			flush()
			continue
		}
		b.WriteByte(c)
	}
	flush()
	return out
}

func DisplayRoots(roots []string, home string) string {
	var parts []string
	for _, r := range roots {
		if home != "" && containsPath(home, r) {
			rel, err := filepath.Rel(home, r)
			if err == nil {
				parts = append(parts, "~/"+filepath.ToSlash(rel))
				continue
			}
		}
		parts = append(parts, r)
	}
	return strings.Join(parts, ", ")
}
