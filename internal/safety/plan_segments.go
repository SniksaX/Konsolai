package safety

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// SplitPlanCommand splits on top-level &&, ;, || and newlines, respecting
// quotes. for/while loops are kept as one segment. Pipes are not split.
func SplitPlanCommand(cmd string) []string {
	var segs []string
	var b strings.Builder
	var word strings.Builder
	var quote byte
	loop := 0
	flushWord := func() {
		w := word.String()
		word.Reset()
		switch w {
		case "for", "while":
			loop++
		case "done":
			if loop > 0 {
				loop--
			}
		}
	}
	flushSeg := func() {
		s := strings.TrimSpace(b.String())
		b.Reset()
		if s != "" {
			segs = append(segs, s)
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			b.WriteByte(c)
			word.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			b.WriteByte(c)
			word.WriteByte(c)
			continue
		}
		if c == '\\' && i+1 < len(cmd) {
			b.WriteByte(c)
			b.WriteByte(cmd[i+1])
			word.WriteByte(c)
			word.WriteByte(cmd[i+1])
			i++
			continue
		}
		if c == ' ' || c == '\t' {
			flushWord()
			b.WriteByte(c)
			continue
		}
		sep := false
		skip := 0
		if c == '\n' || c == '\r' || c == ';' {
			sep = true
		} else if c == '&' && i+1 < len(cmd) && cmd[i+1] == '&' {
			sep, skip = true, 1
		} else if c == '|' && i+1 < len(cmd) && cmd[i+1] == '|' {
			sep, skip = true, 1
		}
		if sep {
			flushWord()
			if loop > 0 {
				b.WriteByte(c)
				if skip == 1 {
					b.WriteByte(cmd[i+1])
					i++
				}
				continue
			}
			flushSeg()
			i += skip
			continue
		}
		b.WriteByte(c)
		word.WriteByte(c)
	}
	flushWord()
	flushSeg()
	return segs
}

type forLoop struct {
	name  string
	items []string
	body  string
}

func parseForLoop(seg string) (forLoop, bool) {
	s := strings.TrimSpace(seg)
	if !strings.HasPrefix(s, "for ") && !strings.HasPrefix(s, "for\t") {
		return forLoop{}, false
	}
	toks := tokenizeWords(s)
	if len(toks) < 6 {
		return forLoop{}, false
	}
	if toks[0].word != "for" || toks[2].word != "in" {
		return forLoop{}, false
	}
	name := unquote(toks[1].word)
	if name == "" || hasDyn(name) {
		return forLoop{}, false
	}
	doIdx, doneIdx := -1, -1
	for i := 3; i < len(toks); i++ {
		if toks[i].word == "do" && doIdx < 0 {
			doIdx = i
		}
		if toks[i].word == "done" {
			doneIdx = i
		}
	}
	if doIdx < 0 || doneIdx <= doIdx {
		return forLoop{}, false
	}
	var items []string
	for _, t := range toks[3:doIdx] {
		items = append(items, unquote(t.word))
	}
	body := strings.TrimSpace(s[toks[doIdx].end:toks[doneIdx].start])
	body = strings.Trim(body, "; \t\n\r")
	if body == "" || len(items) == 0 {
		return forLoop{}, false
	}
	return forLoop{name: name, items: items, body: body}, true
}

func substFor(body, name, val string) string {
	body = strings.ReplaceAll(body, "${"+name+"}", val)
	body = strings.ReplaceAll(body, `"$`+name+`"`, val)
	body = strings.ReplaceAll(body, "$"+name, val)
	return body
}

func skipFlags(args []string) []string {
	var out []string
	dashdash := false
	for _, a := range args {
		if !dashdash && a == "--" {
			dashdash = true
			continue
		}
		if !dashdash && strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func chmodPaths(args []string) []string {
	skipMode := true
	var out []string
	for _, a := range args {
		if a == "--" {
			skipMode = false
			continue
		}
		if skipMode && strings.HasPrefix(a, "-") && a != "-" && !isChmodMode(a) {
			continue
		}
		if skipMode {
			skipMode = false
			continue
		}
		out = append(out, a)
	}
	return out
}

func isChmodMode(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '+' || s[0] == '-' || s[0] == '=' {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '7' {
			return false
		}
	}
	return len(s) >= 3 && len(s) <= 4
}

func redirectTargets(seg string) []string {
	var out []string
	var quote byte
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '\\' && i+1 < len(seg) {
			i++
			continue
		}
		if c != '>' {
			continue
		}
		if i > 0 && (seg[i-1] == '>' || seg[i-1] == '2' || seg[i-1] == '&') {
			continue
		}
		j := i + 1
		if j < len(seg) && seg[j] == '>' {
			j++
		}
		for j < len(seg) && (seg[j] == ' ' || seg[j] == '\t') {
			j++
		}
		if j >= len(seg) {
			break
		}
		start := j
		if seg[j] == '\'' || seg[j] == '"' {
			q := seg[j]
			j++
			start = j
			for j < len(seg) && seg[j] != q {
				j++
			}
			out = append(out, seg[start:j])
			if j < len(seg) {
				j++
			}
			i = j - 1
			continue
		}
		for j < len(seg) && !unicode.IsSpace(rune(seg[j])) && seg[j] != ';' && seg[j] != '&' && seg[j] != '|' {
			j++
		}
		out = append(out, seg[start:j])
		i = j - 1
	}
	return out
}

func stripRedirects(seg string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if c == '>' {
			if i > 0 && (seg[i-1] == '2' || seg[i-1] == '&') {
				b.WriteByte(c)
				continue
			}
			j := i + 1
			if j < len(seg) && seg[j] == '>' {
				j++
			}
			for j < len(seg) && (seg[j] == ' ' || seg[j] == '\t') {
				j++
			}
			if j < len(seg) && (seg[j] == '\'' || seg[j] == '"') {
				q := seg[j]
				j++
				for j < len(seg) && seg[j] != q {
					j++
				}
				if j < len(seg) {
					j++
				}
			} else {
				for j < len(seg) && !unicode.IsSpace(rune(seg[j])) && seg[j] != ';' && seg[j] != '&' && seg[j] != '|' {
					j++
				}
			}
			i = j - 1
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func expandLiteralPaths(t string) ([]string, bool) {
	t = strings.TrimSpace(t)
	if t == "" || hasDyn(t) {
		return nil, false
	}
	ex, ok := expandBraces(t)
	if !ok {
		return nil, false
	}
	home, _ := os.UserHomeDir()
	var out []string
	for _, p := range ex {
		if hasDyn(p) {
			return nil, false
		}
		out = append(out, expandTilde(p, home))
	}
	return out, true
}

func expandTilde(p, home string) string {
	if home == "" {
		return p
	}
	if exp, ok := expandHome(p, home); ok {
		return filepath.ToSlash(filepath.Clean(exp))
	}
	return p
}

func expandBraces(s string) ([]string, bool) {
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return []string{s}, true
	}
	j := matchBrace(s, i)
	if j < 0 {
		return nil, false
	}
	inner := s[i+1 : j]
	parts := splitBraceList(inner)
	if len(parts) < 2 {
		return []string{s}, true
	}
	var out []string
	for _, part := range parts {
		nested, ok := expandBraces(s[:i] + part + s[j+1:])
		if !ok {
			return nil, false
		}
		out = append(out, nested...)
	}
	return out, true
}

func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitBraceList(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func hasDyn(s string) bool {
	if strings.Contains(s, "`") || strings.Contains(s, "$(") {
		return true
	}
	return strings.Contains(s, "$")
}

func gitArgs(seg string) (sub string, rest []string, ok bool) {
	toks := tokenizeWords(seg)
	if len(toks) < 2 {
		return "", nil, false
	}
	if filepath.Base(unquote(toks[0].word)) != "git" {
		return "", nil, false
	}
	args := make([]string, 0, len(toks)-1)
	for _, t := range toks[1:] {
		args = append(args, unquote(t.word))
	}
	return args[0], args[1:], true
}

func flagArg(args []string, flags ...string) (string, bool) {
	want := map[string]bool{}
	for _, f := range flags {
		want[f] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if want[a] {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		for _, f := range flags {
			if strings.HasPrefix(a, f) && len(a) > len(f) && (a[len(f)] == '=' || len(f) == 2) {
				v := a[len(f):]
				v = strings.TrimPrefix(v, "=")
				return v, true
			}
		}
	}
	return "", false
}

func firstBranch(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if a == "." || strings.Contains(a, "/") {
			return ""
		}
		return a
	}
	return ""
}

func commitMessage(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--message":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--message="):
			return strings.TrimPrefix(a, "--message=")
		case strings.HasPrefix(a, "-m") && len(a) > 2:
			return a[2:]
		}
	}
	return ""
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
