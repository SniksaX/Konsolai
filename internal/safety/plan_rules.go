package safety

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
