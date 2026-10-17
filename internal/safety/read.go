package safety

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrSensitiveFile is returned when a read targets a denylisted path.
var ErrSensitiveFile = errors.New("sensitive file")

var sensitiveHomeSubs = []string{
	".ssh",
	".gnupg",
	filepath.Join(".local", "share", "keyrings"),
	".mozilla",
	filepath.Join(".config", "BraveSoftware"),
	filepath.Join(".config", "google-chrome"),
	filepath.Join(".config", "chromium"),
	filepath.Join(".config", "konsolai"),
}

var sensitiveExact = map[string]bool{
	".netrc":  true,
	".pgpass": true,
}

var sensitiveExt = []string{
	".key", ".pem", ".p12", ".pfx", ".kdbx",
}

// ResolveRead resolves p for a read-only tool. Any user-readable path is
// allowed except the sensitive read denylist.
func (g *Guard) ResolveRead(p string) (string, error) {
	abs, err := g.resolveAny(p)
	if err != nil {
		return "", err
	}
	if SensitiveRead(abs) || g.ExtraReadMatch(abs) {
		return "", ErrSensitiveFile
	}
	return abs, nil
}

// ResolveReadDir resolves p as a directory listing target. Sensitive files
// inside it still appear by name; reading their contents is denied separately.
func (g *Guard) ResolveReadDir(p string) (string, error) {
	return g.resolveAny(p)
}

// Contains reports whether abs is inside the write root.
func (g *Guard) Contains(abs string) bool {
	if g == nil {
		return false
	}
	return containsPath(g.root, abs)
}

func (g *Guard) resolveAny(p string) (string, error) {
	raw := p
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &DeniedError{Rule: "tilde", Detail: raw}
		}
		exp, ok := expandHome(p, home)
		if !ok {
			return "", &DeniedError{Rule: "tilde", Detail: raw}
		}
		p = exp
	}
	if p == "" {
		p = "."
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(g.base, p))
	}
	resolved, err := evalDeepest(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// SensitiveRead reports whether abs is in the sensitive read denylist.
func SensitiveRead(abs string) bool {
	abs = filepath.Clean(abs)
	if isProcEnviron(abs) {
		return true
	}
	base := filepath.Base(abs)
	if sensitiveExact[base] {
		return true
	}
	if strings.HasPrefix(base, "id_") {
		return true
	}
	lower := strings.ToLower(base)
	for _, ext := range sensitiveExt {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	home = filepath.Clean(home)
	for _, sub := range sensitiveHomeSubs {
		deny := filepath.Join(home, sub)
		if containsPath(deny, abs) {
			return true
		}
	}
	return false
}

func isProcEnviron(abs string) bool {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/proc/") {
		return false
	}
	rest := strings.TrimPrefix(p, "/proc/")
	pid, env, ok := strings.Cut(rest, "/")
	if !ok || env != "environ" || pid == "" {
		return false
	}
	for _, r := range pid {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
