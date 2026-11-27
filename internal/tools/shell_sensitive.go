package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SniksaX/Konsolai/internal/safety"
)

var (
	reRedirect   = regexp.MustCompile(`(?:^|[^>])>>?\s*("?)([^\s;&|"'<>]+)`)
	reTee        = regexp.MustCompile(`(?:^|[;&|]\s*)tee\s+(?:-a\s+)?("?)([^\s;&|"'<>]+)`)
	reSedEdit    = regexp.MustCompile(`(?:^|[;&|]\s*)sed\s+-i[^\s]*\s+(?:[^;&|]*?\s+)?("?)([^\s;&|"'<>]+)`)
	reMove       = regexp.MustCompile(`(?:^|[;&|]\s*)(?:mv|cp|ln|install|truncate)\b[^;&|]*?\s("?)([^\s;&|"'<>]+)`)
	reDD         = regexp.MustCompile(`(?:^|[;&|]\s*)dd\b[^;&|]*?\bof=("?)([^\s;&|"'<>]+)`)
	reInterp     = regexp.MustCompile(`(?:open|writeFileSync|write_text|write_bytes)\(\s*["']([^"']+)["']`)
	gitWriteVerb = regexp.MustCompile(`\bgit\s+config\b`)
)

// shellSensitive analyses the statically visible write targets of one shell
// command. It returns never (git metadata) or a confirm reason naming files.
func shellSensitive(guard *safety.Guard, cmd, cwd string, extra []string) (never bool, reason string) {
	if guard == nil {
		return false, ""
	}
	if gitWriteTarget(cmd) {
		return true, "this is git metadata; the user changes it themselves"
	}
	var neverFiles, confirmFiles []string
	base := cwd
	seen := map[string]bool{}
	addTarget := func(target string) {
		target = strings.TrimSpace(strings.Trim(target, `"'`))
		if target == "" || strings.HasPrefix(target, "$") || strings.HasPrefix(target, "/dev/") || target == "&1" || target == "&2" {
			return
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		resolved := safety.ResolvePhysical(target)
		if resolved == "" || seen[resolved] {
			return
		}
		seen[resolved] = true
		class, why := safety.ClassifyWriteAt(guard.Root(), resolved, extra)
		switch class {
		case safety.WriteNever:
			neverFiles = append(neverFiles, resolved)
		case safety.WriteConfirm:
			confirmFiles = append(confirmFiles, resolved+" ("+why+")")
		}
	}
	for _, re := range []*regexp.Regexp{reRedirect, reTee, reSedEdit, reMove, reDD, reInterp} {
		for _, m := range re.FindAllStringSubmatch(cmd, -1) {
			if len(m) >= 3 {
				addTarget(m[2])
			} else if len(m) == 2 {
				addTarget(m[1])
			}
		}
	}
	// Copy/move/link verbs take their destination as the last argument.
	for _, seg := range regexp.MustCompile(`[;&|]+`).Split(cmd, -1) {
		fields := strings.Fields(seg)
		if len(fields) < 3 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "mv", "cp", "ln", "install", "truncate":
			addTarget(fields[len(fields)-1])
		case "sed":
			if len(fields) >= 4 && strings.HasPrefix(fields[1], "-i") {
				addTarget(fields[len(fields)-1])
			}
		}
	}
	if len(neverFiles) > 0 {
		return true, "this is git metadata; the user changes it themselves"
	}
	if len(confirmFiles) > 0 {
		return false, "runs code later: " + strings.Join(confirmFiles, ", ")
	}
	if note := safety.InstallNote(cmd); note != "" {
		// Non-blocking install note is appended to the preview by the caller.
		return false, ""
	}
	return false, ""
}

// gitWriteTarget reports git commands that change git metadata/config.
func gitWriteTarget(cmd string) bool {
	if !strings.Contains(cmd, "git") {
		return false
	}
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == "git" {
			rest := fields[i+1:]
			if len(rest) == 0 {
				return false
			}
			switch rest[0] {
			case "config":
				for _, a := range rest[1:] {
					switch a {
					case "--get", "--get-all", "--get-regexp", "--list", "-l":
						return false
					}
				}
				return true
			case "init":
				for _, a := range rest[1:] {
					if strings.HasPrefix(a, "--template") {
						return true
					}
				}
				return false
			case "update-index":
				for _, a := range rest[1:] {
					if a == "--chmod" || strings.HasPrefix(a, "--chmod=") {
						return true
					}
				}
			}
		}
	}
	return false
}

// installNoteFor returns the plan/confirm preview line for installing commands.
func installNoteFor(cmd string) string {
	if note := safety.InstallNote(cmd); note != "" {
		return "note: " + note + " (this can run code later)"
	}
	return ""
}

var _ = fmt.Sprintf
