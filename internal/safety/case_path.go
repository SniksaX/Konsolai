package safety

import (
	"os"
	"path/filepath"
	"strings"
)

// SuggestPath returns the case-corrected form of an absolute or ~ path that
// does not exist but whose existing components differ only by letter case.
// Relative paths, paths that exist, and paths without an unambiguous case
// variant are left alone. The suggestion keeps the input's shape: a ~ path
// stays a ~ path and an absolute path stays absolute.
func SuggestPath(raw, home string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	var abs string
	switch {
	case strings.HasPrefix(raw, "~"):
		if home == "" {
			if h, err := os.UserHomeDir(); err == nil {
				home = h
			}
		}
		exp, ok := expandHome(raw, home)
		if !ok {
			return "", false
		}
		abs = filepath.Clean(exp)
	case filepath.IsAbs(raw):
		abs = filepath.Clean(raw)
	default:
		return "", false
	}
	corrected, ok := caseVariant(abs)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(raw, "~") && home != "" {
		if rel, err := filepath.Rel(filepath.Clean(home), corrected); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "~/" + filepath.ToSlash(rel), true
		}
	}
	return corrected, true
}

// caseVariant rewrites each missing component of a path to the single existing
// directory entry that differs only by letter case. Components below the last
// correction keep their given spelling, so a new file in a mistyped directory
// still gets a suggestion.
func caseVariant(abs string) (string, bool) {
	abs = filepath.Clean(abs)
	if _, err := os.Lstat(abs); err == nil {
		return "", false
	}
	missing := []string{}
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
		if _, err := os.Lstat(cur); err == nil {
			break
		}
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	corrected := cur
	changed := false
	for i := 0; i < len(missing); i++ {
		exact := filepath.Join(corrected, missing[i])
		if _, err := os.Lstat(exact); err == nil {
			corrected = exact
			continue
		}
		match, ok := caseSibling(corrected, missing[i])
		if !ok {
			for _, rest := range missing[i:] {
				corrected = filepath.Join(corrected, rest)
			}
			break
		}
		corrected = filepath.Join(corrected, match)
		changed = true
	}
	if !changed || corrected == abs {
		return "", false
	}
	return corrected, true
}

// caseSibling returns the one entry in dir whose name equals name ignoring
// case. Two such entries are ambiguous and produce no suggestion.
func caseSibling(dir, name string) (string, bool) {
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	match := ""
	for _, e := range entries {
		if e.Name() == name {
			return name, true
		}
		if strings.EqualFold(e.Name(), name) {
			if match != "" {
				return "", false
			}
			match = e.Name()
		}
	}
	if match == "" {
		return "", false
	}
	return match, true
}
