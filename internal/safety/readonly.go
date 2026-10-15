package safety

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	reCommandSub = regexp.MustCompile(`\$\(`)
	reHeredoc    = regexp.MustCompile(`<<`)
	reSedWrite   = regexp.MustCompile(`(?:^|[;\n])\s*w\s+\S+|/w(?:\s|$)`)
	reSedQuotedW = regexp.MustCompile(`['"]w\s+\S+`)
	reAwkSystem  = regexp.MustCompile(`(?i)system\s*\(`)
	reAssign     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// IsReadOnlyCommand reports whether cmd is a conservative read-only shell command.
func IsReadOnlyCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	if strings.Contains(cmd, "`") || reCommandSub.MatchString(cmd) || reHeredoc.MatchString(cmd) {
		return false
	}
	if hasBadRedirect(cmd) || hasBareAmpersand(cmd) || hasSubshellAssignment(cmd) {
		return false
	}
	segs := SplitSegments(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if !isReadOnlySegment(seg) {
			return false
		}
	}
	return true
}

// SplitSegments splits a shell command on newlines, ;, &&, || and |.
func SplitSegments(cmd string) []string {
	var segs []string
	var b strings.Builder
	flush := func() {
		s := strings.TrimSpace(b.String())
		b.Reset()
		if s != "" {
			segs = append(segs, s)
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if c == '\n' || c == '\r' || c == ';' {
			flush()
			continue
		}
		if c == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
			flush()
			i++
			continue
		}
		if c == '|' && i+1 < len(cmd) && cmd[i+1] == '|' {
			flush()
			i++
			continue
		}
		if c == '|' {
			flush()
			continue
		}
		b.WriteByte(c)
	}
	flush()
	return segs
}

// SegmentBinary returns the basename of the first non-assignment word in seg.
func SegmentBinary(seg string) string {
	for _, w := range strings.Fields(seg) {
		if reAssign.MatchString(w) {
			continue
		}
		return filepath.Base(w)
	}
	return ""
}

func isReadOnlySegment(seg string) bool {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return false
	}
	words := strings.Fields(seg)
	if len(words) == 0 {
		return false
	}
	if reAssign.MatchString(words[0]) {
		return false
	}
	cmd := filepath.Base(words[0])
	args := words[1:]
	switch cmd {
	case "ls", "pwd", "cat", "head", "tail", "wc", "file", "stat", "du", "df", "free",
		"uname", "whoami", "id", "hostname", "date", "uptime", "which", "type",
		"env", "printenv", "echo", "ps", "pgrep", "ss", "lsof", "ip", "dig", "nslookup",
		"grep", "rg", "tree", "sort", "uniq", "cut", "tr", "jq", "diff", "cmp",
		"md5sum", "sha256sum", "realpath", "readlink", "basename", "dirname",
		"journalctl":
		return true
	case "xargs":
		return false
	case "find":
		return findOK(args)
	case "awk":
		return !reAwkSystem.MatchString(seg)
	case "sed":
		return sedOK(args, seg)
	case "command":
		return len(args) > 0 && args[0] == "-v"
	case "ping":
		return hasDashC(args)
	case "systemctl":
		return firstSub(args) == "status"
	case "docker":
		return dockerOK(args)
	case "git":
		return gitOK(args)
	case "go":
		return goOK(args)
	case "node":
		return nodeOK(args)
	case "npm":
		return npmOK(args)
	case "pnpm":
		return pnpmOK(args)
	case "python3":
		return hasWord(args, "--version")
	case "pip":
		sub := firstSub(args)
		return sub == "list" || sub == "show"
	case "cargo", "rustc":
		return hasWord(args, "--version")
	case "pacman":
		return pacmanOK(args)
	default:
		return false
	}
}

func findOK(args []string) bool {
	for _, w := range args {
		switch {
		case w == "-delete", w == "-exec", w == "-execdir", w == "-ok":
			return false
		case strings.HasPrefix(w, "-fprint"):
			return false
		}
	}
	return true
}

func sedOK(args []string, seg string) bool {
	for _, w := range args {
		if w == "-i" || (strings.HasPrefix(w, "-i") && len(w) > 2) {
			return false
		}
	}
	return !reSedWrite.MatchString(seg) && !reSedQuotedW.MatchString(seg)
}

func dockerOK(args []string) bool {
	switch firstSub(args) {
	case "ps", "images", "logs", "inspect":
		return true
	default:
		return false
	}
}

func gitOK(args []string) bool {
	sub := firstSub(args)
	switch sub {
	case "status", "log", "diff", "show", "branch", "rev-parse", "ls-files", "blame":
		return true
	case "remote":
		return hasWord(args, "-v") || hasWord(args, "--verbose")
	default:
		return false
	}
}

func goOK(args []string) bool {
	switch firstSub(args) {
	case "version", "env", "list", "doc":
		return true
	default:
		return false
	}
}

func nodeOK(args []string) bool {
	return hasWord(args, "-v") || hasWord(args, "--version")
}

func npmOK(args []string) bool {
	if hasWord(args, "-v") {
		return true
	}
	sub := firstSub(args)
	return sub == "ls" || sub == "view"
}

func pnpmOK(args []string) bool {
	if hasWord(args, "-v") {
		return true
	}
	return firstSub(args) == "ls"
}

func pacmanOK(args []string) bool {
	for _, w := range args {
		if strings.HasPrefix(w, "-Q") || w == "-Ss" || w == "-Si" {
			return true
		}
	}
	return false
}

func firstSub(args []string) string {
	for _, w := range args {
		if strings.HasPrefix(w, "-") {
			continue
		}
		return w
	}
	return ""
}

func hasWord(args []string, want string) bool {
	for _, w := range args {
		if w == want {
			return true
		}
	}
	return false
}

func hasDashC(args []string) bool {
	for _, w := range args {
		if w == "-c" {
			return true
		}
		if strings.HasPrefix(w, "-c") && len(w) > 2 && unicode.IsDigit(rune(w[2])) {
			return true
		}
	}
	return false
}

func hasBadRedirect(s string) bool {
	t := strings.ReplaceAll(s, "2>/dev/null", "")
	t = strings.ReplaceAll(t, "2>&1", "")
	return strings.Contains(t, ">")
}

func hasBareAmpersand(s string) bool {
	t := strings.ReplaceAll(s, "&&", "")
	t = strings.ReplaceAll(t, "2>&1", "")
	t = strings.ReplaceAll(t, "&>", "")
	return strings.Contains(t, "&")
}

func hasSubshellAssignment(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '(' {
			continue
		}
		if i > 0 && s[i-1] == '$' {
			continue
		}
		end := matchParen(s, i)
		if end < 0 {
			return true
		}
		inner := s[i+1 : end]
		for _, seg := range SplitSegments(inner) {
			words := strings.Fields(seg)
			if len(words) > 0 && reAssign.MatchString(words[0]) {
				return true
			}
		}
		i = end
	}
	return false
}

func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
