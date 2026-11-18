package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// CheckCommandPaths resolves path words against dir. A mutating segment must
// stay inside the write roots. A read-only segment may read anywhere the user
// can, except the sensitive denylist, which the file tools already enforce.
func (g *Guard) CheckCommandPaths(cmd, dir string) error {
	if g == nil {
		return nil
	}
	if dir == "" {
		dir = g.base
	}
	for _, seg := range SplitSegments(cmd) {
		if IsReadOnlyCommand(seg) {
			if err := g.checkReadSegment(seg, dir); err != nil {
				return err
			}
			continue
		}
		for _, w := range strings.Fields(seg) {
			for _, p := range wordPaths(w) {
				if _, err := g.resolve(p, dir); err != nil {
					return err
				}
			}
		}
		// A quoted -c script hides its redirects from the word scan.
		if shellInterpreters[SegmentBinary(seg)] {
			for _, p := range scriptRedirectTargets(seg) {
				if _, err := g.resolve(p, dir); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// shellInterpreters run a quoted script, so their redirects are invisible to
// strings.Fields and are checked separately.
var shellInterpreters = map[string]bool{
	"bash": true, "sh": true, "dash": true, "zsh": true,
	"python": true, "python3": true, "perl": true, "ruby": true,
	"node": true, "php": true, "eval": true,
}

var redirectRe = regexp.MustCompile(`[0-9]*>>?\s*("[^"]*"|'[^']*'|[^\s;|&<>]+)`)

// scriptRedirectTargets lists the literal files a quoted shell script redirects
// to; the plain redirectTargets skips quoted text on purpose.
func scriptRedirectTargets(seg string) []string {
	var out []string
	for _, m := range redirectRe.FindAllStringSubmatch(seg, -1) {
		t := unquoteWord(strings.TrimSpace(m[1]))
		// A redirect at the end of a quoted script keeps the closing quote.
		t = strings.Trim(t, `"'`)
		if t == "" || strings.HasPrefix(t, "&") || t == "-" {
			continue
		}
		if strings.ContainsAny(t, "$`*?[") || strings.Contains(t, "://") {
			continue
		}
		if benignPath(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// checkReadSegment denies a read-only segment whose statically visible path
// words name a sensitive file. The shell expands globs and substitutions
// itself, so only literal paths and a glob's literal parent are checked.
func (g *Guard) checkReadSegment(seg, dir string) error {
	for _, p := range readTargets(seg) {
		raw, err := g.resolveRaw(p, dir)
		if err != nil {
			continue
		}
		if SensitiveRead(raw) || g.ExtraReadMatch(raw) {
			return fmt.Errorf("safety: read %s: %w", p, ErrSensitiveFile)
		}
		abs, err := evalDeepest(raw)
		if err != nil {
			continue
		}
		if SensitiveRead(abs) || g.ExtraReadMatch(abs) {
			return fmt.Errorf("safety: read %s: %w", p, ErrSensitiveFile)
		}
	}
	return nil
}

// readTargets lists the path words of a read-only segment that can be checked
// without running the shell. Flags, bare words and expansions are skipped.
func readTargets(seg string) []string {
	var out []string
	for _, w := range strings.Fields(seg) {
		w = unquoteWord(strings.TrimSpace(peelRedirect(w)))
		if w == "" || (strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--")) {
			continue
		}
		if strings.HasPrefix(w, "--") {
			i := strings.IndexByte(w, '=')
			if i < 0 {
				continue
			}
			w = w[i+1:]
		}
		if strings.ContainsAny(w, "$`()|&;<>!\"'") || strings.Contains(w, "://") {
			continue
		}
		w = globLiteral(w)
		if w == "" {
			continue
		}
		if !strings.HasPrefix(w, "~") && !filepath.IsAbs(w) && !strings.Contains(w, "/") && !strings.HasPrefix(w, "..") {
			continue
		}
		out = append(out, w)
	}
	return out
}

// globLiteral cuts a word at its first glob character and returns the literal
// parent. "~/.ssh/*" becomes "~/.ssh"; "*.go" becomes empty.
func globLiteral(w string) string {
	if i := strings.IndexAny(w, "*?[{"); i >= 0 {
		w = w[:i]
	}
	return strings.TrimRight(w, "/")
}

// resolveRaw expands a ~ path and joins a relative one onto the shell working
// directory, without resolving symlinks. The symlink target is checked too, but
// the written path must also be judged: a denylisted location stays denylisted
// even when it links into the project.
func (g *Guard) resolveRaw(p, dir string) (string, error) {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", &DeniedError{Rule: "tilde", Detail: p}
		}
		exp, ok := expandHome(p, home)
		if !ok {
			return "", &DeniedError{Rule: "tilde", Detail: p}
		}
		p = exp
	}
	if p == "" {
		p = "."
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Clean(filepath.Join(dir, p)), nil
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
