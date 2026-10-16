package safety

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const PrivilegedPlanMsg = "you cannot run privileged commands; put them in finish under 'Run these yourself:' with the exact command"

var privilegedBins = map[string]bool{
	"sudo": true, "su": true, "doas": true, "pkexec": true,
}

var launcherPrefixes = []string{
	"npx -y ",
	"npx ",
	"pnpm exec ",
	"pnpm dlx ",
	"npm exec -- ",
}

var yarnCommands = map[string]bool{
	"install": true, "add": true, "remove": true, "run": true, "init": true,
	"test": true, "build": true, "start": true, "dev": true, "workspace": true,
	"workspaces": true, "dlx": true, "exec": true, "create": true, "global": true,
	"upgrade": true, "outdated": true, "why": true, "info": true, "config": true,
	"cache": true, "help": true, "version": true, "publish": true, "link": true,
	"unlink": true, "pack": true,
}

// Plan is the approved plan used to match run_shell commands.
type Plan struct {
	Files    []string
	Commands []string
	Text     string
}

// SegHit is the matching rule for one command segment.
type SegHit struct {
	Segment string
	Rule    string
	OK      bool
}

// MatchPlan reports whether cmd is in the approved plan (R1–R4).
func MatchPlan(p Plan, cmd string) (bool, []SegHit) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false, nil
	}
	if matchDeclared(p.Commands, cmd) {
		return true, []SegHit{{Segment: cmd, Rule: "R2 " + headline(cmd), OK: true}}
	}
	segs := SplitPlanCommand(cmd)
	if len(segs) == 0 {
		return false, nil
	}
	hits := make([]SegHit, 0, len(segs))
	all := true
	for _, s := range segs {
		h := matchSeg(p, s)
		hits = append(hits, h)
		if !h.OK {
			all = false
		}
	}
	return all, hits
}

func matchSeg(p Plan, seg string) SegHit {
	seg = strings.TrimSpace(seg)
	if gitNever(seg) {
		return SegHit{Segment: seg, Rule: "R4 " + headline(seg), OK: false}
	}
	if IsReadOnlyCommand(seg) {
		return SegHit{Segment: seg, Rule: "R1 " + headline(seg), OK: true}
	}
	if matchDeclared(p.Commands, seg) {
		return SegHit{Segment: seg, Rule: "R2 " + headline(seg), OK: true}
	}
	if kind, targets, ok := fileFamily(p, seg); ok {
		return SegHit{Segment: seg, Rule: "R3 " + kind + " " + strings.Join(targets, " "), OK: true}
	}
	if desc, ok := gitFamily(p, seg); ok {
		return SegHit{Segment: seg, Rule: "R4 " + desc, OK: true}
	}
	return SegHit{Segment: seg, Rule: "R? " + headline(seg), OK: false}
}

func matchDeclared(cmds []string, cmd string) bool {
	want := EquivCommand(cmd)
	for _, c := range cmds {
		if EquivCommand(c) == want {
			return true
		}
		for _, s := range SplitPlanCommand(c) {
			if EquivCommand(s) == want {
				return true
			}
		}
	}
	return false
}

// EquivCommand whitespace- and wrapper-normalizes a shell command.
func EquivCommand(s string) string {
	return stripLauncher(strings.Join(strings.Fields(s), " "))
}

func stripLauncher(s string) string {
	lower := strings.ToLower(s)
	for _, p := range launcherPrefixes {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSpace(s[len(p):])
		}
	}
	if strings.HasPrefix(lower, "yarn ") {
		rest := strings.TrimSpace(s[5:])
		first := rest
		if i := strings.IndexByte(rest, ' '); i >= 0 {
			first = rest[:i]
		}
		if first != "" && !yarnCommands[strings.ToLower(first)] {
			return rest
		}
	}
	return s
}

func headline(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

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

func fileFamily(p Plan, seg string) (string, []string, bool) {
	if fl, ok := parseForLoop(seg); ok {
		return fileFamilyFor(p, fl)
	}
	kind, raw, ok := fileFamilyRaw(seg)
	if !ok {
		return "", nil, false
	}
	var targets []string
	for _, t := range raw {
		ex, ok := expandLiteralPaths(t)
		if !ok {
			return "", nil, false
		}
		targets = append(targets, ex...)
	}
	if len(targets) == 0 {
		return "", nil, false
	}
	if !allPlanned(p.Files, targets) {
		return "", nil, false
	}
	return kind, displayTargets(targets), true
}

func fileFamilyFor(p Plan, fl forLoop) (string, []string, bool) {
	var items []string
	for _, it := range fl.items {
		ex, ok := expandLiteralPaths(it)
		if !ok {
			return "", nil, false
		}
		items = append(items, ex...)
	}
	if len(items) == 0 {
		return "", nil, false
	}
	var allTargets []string
	kind := ""
	for _, it := range items {
		body := substFor(fl.body, fl.name, it)
		if hasDyn(body) {
			return "", nil, false
		}
		okBody := true
		for _, sub := range SplitPlanCommand(body) {
			if IsReadOnlyCommand(sub) {
				continue
			}
			k, ts, ok := fileFamily(p, sub)
			if !ok {
				okBody = false
				break
			}
			if kind == "" {
				kind = k
			}
			allTargets = append(allTargets, ts...)
		}
		if !okBody {
			return "", nil, false
		}
	}
	if kind == "" {
		kind = "for"
	}
	return kind, displayTargets(allTargets), true
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

func fileFamilyRaw(seg string) (string, []string, bool) {
	if ts := redirectTargets(seg); len(ts) > 0 {
		rest := stripRedirects(seg)
		rest = strings.TrimSpace(rest)
		if rest == "" || rest == ":" || strings.HasPrefix(rest, "printf ") || rest == "printf" || rest == "true" {
			return "redirect", ts, true
		}
		bin := SegmentBinary(rest)
		if bin == "printf" || bin == ":" || bin == "true" || bin == "echo" {
			return "redirect", ts, true
		}
	}
	toks := tokenizeWords(seg)
	if len(toks) == 0 {
		return "", nil, false
	}
	words := make([]string, len(toks))
	for i, t := range toks {
		words[i] = unquote(t.word)
	}
	cmd := filepath.Base(words[0])
	args := words[1:]
	switch cmd {
	case "mkdir":
		return "mkdir", skipFlags(args), true
	case "touch":
		return "touch", skipFlags(args), true
	case "cp", "mv":
		paths := skipFlags(args)
		if len(paths) < 1 {
			return "", nil, false
		}
		return cmd, []string{paths[len(paths)-1]}, true
	case "chmod":
		paths := chmodPaths(args)
		if len(paths) == 0 {
			return "", nil, false
		}
		return "chmod", paths, true
	default:
		return "", nil, false
	}
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

func allPlanned(files, targets []string) bool {
	if len(targets) == 0 {
		return false
	}
	norms := plannedPaths(files)
	if len(norms) == 0 {
		return false
	}
	for _, t := range targets {
		if !plannedTarget(norms, t) {
			return false
		}
	}
	return true
}

func plannedPaths(files []string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		out = append(out, normPlanPath(expandTilde(f, home)))
	}
	return out
}

func normPlanPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return strings.TrimRight(p, "/")
}

func plannedTarget(files []string, target string) bool {
	t := normPlanPath(target)
	for _, f := range files {
		if f == t {
			return true
		}
		if strings.HasPrefix(f, t+"/") {
			return true
		}
	}
	return false
}

func displayTargets(ts []string) []string {
	home, _ := os.UserHomeDir()
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t
		if home != "" && (t == home || strings.HasPrefix(t, home+"/")) {
			out[i] = "~" + strings.TrimPrefix(t, home)
		}
	}
	return out
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

func gitNever(seg string) bool {
	sub, rest, ok := gitArgs(seg)
	if !ok {
		return false
	}
	switch sub {
	case "push", "rebase":
		return true
	case "reset":
		return hasWord(rest, "--hard")
	case "branch":
		return hasWord(rest, "-D") || hasWord(rest, "-d")
	case "checkout":
		return hasWord(rest, "--")
	}
	return false
}

func gitFamily(p Plan, seg string) (string, bool) {
	sub, rest, ok := gitArgs(seg)
	if !ok || gitNever(seg) {
		return "", false
	}
	text := p.Text + "\n" + strings.Join(p.Commands, "\n")
	switch sub {
	case "checkout":
		if b, ok := flagArg(rest, "-b", "-B"); ok {
			if b != "" && strings.Contains(text, b) {
				return "checkout -b " + b, true
			}
			return "", false
		}
		if b := firstBranch(rest); b != "" && strings.Contains(text, b) {
			return "checkout " + b, true
		}
	case "switch":
		if b, ok := flagArg(rest, "-c", "-C"); ok {
			if b != "" && strings.Contains(text, b) {
				return "switch -c " + b, true
			}
			return "", false
		}
		if b := firstBranch(rest); b != "" && strings.Contains(text, b) {
			return "switch " + b, true
		}
	case "add":
		if strings.Contains(text, "git commit") {
			return "git add", true
		}
	case "commit":
		msg := commitMessage(rest)
		if msg != "" && strings.Contains(text, msg) {
			return "git commit", true
		}
	}
	return "", false
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

// PlanCommandForbidden reports whether a planned command is privileged or denylisted.
func PlanCommandForbidden(cmd string) error {
	if err := (*Guard)(nil).CheckCommand(cmd); err != nil {
		return errors.New(PrivilegedPlanMsg)
	}
	for _, seg := range SplitPlanCommand(cmd) {
		if privilegedBins[SegmentBinary(seg)] {
			return errors.New(PrivilegedPlanMsg)
		}
		for _, w := range strings.Fields(seg) {
			if privilegedBins[filepath.Base(w)] {
				return errors.New(PrivilegedPlanMsg)
			}
		}
	}
	return nil
}

// AllowPlanFiles adds approved plan file paths (and parent dirs) as write roots.
func (g *Guard) AllowPlanFiles(files []string) {
	if g == nil {
		return
	}
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var abs string
		if strings.HasPrefix(f, "~") {
			exp, ok := expandHome(f, home)
			if !ok {
				continue
			}
			abs = filepath.Clean(exp)
		} else if filepath.IsAbs(f) {
			abs = filepath.Clean(f)
		} else {
			continue
		}
		for _, p := range planWriteRoots(abs, home) {
			if seen[p] {
				continue
			}
			seen[p] = true
			g.extra = append(g.extra, p)
		}
	}
}

func planWriteRoots(abs, home string) []string {
	if underDeniedHome(abs, home) {
		return nil
	}
	out := []string{abs}
	parent := filepath.Dir(abs)
	if parent == abs || (home != "" && parent == home) || underDeniedHome(parent, home) {
		return out
	}
	out = append(out, parent)
	cur := filepath.Dir(parent)
	for home != "" && cur != parent && cur != home && containsPath(home, cur) && !underDeniedHome(cur, home) {
		out = append(out, cur)
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return out
}
