package safety

import (
	"os"
	"path/filepath"
	"strings"
)

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
