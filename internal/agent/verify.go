package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// VerifiedCall is a successful tool result from the current turn.
type VerifiedCall struct {
	Tool string
	Args string
	Out  string
}

type claimRule struct {
	name string
	re   *regexp.Regexp
	ok   func([]VerifiedCall) bool
}

var finishClaimRules = []claimRule{
	{
		name: "killed/stopped",
		re:   regexp.MustCompile(`(?i)\b(killed|terminated|stopped|arrêté|arrete|tué)\b`),
		ok:   shellMatched(`(?i)\b(kill|pkill|killall|fuser|systemctl\s+stop)\b`),
	},
	{
		name: "started",
		re:   regexp.MustCompile(`(?i)\b(started|launched|démarré|demarre)\b`),
		ok:   shellMatched(`(?i)\b(systemctl\s+start|service\s+\S+\s+start)\b`),
	},
	{
		name: "created/wrote",
		re:   regexp.MustCompile(`(?i)\b(created|wrote|written|créé|cree)\b`),
		ok: func(calls []VerifiedCall) bool {
			return hasTool(calls, "write_file", "edit_file")
		},
	},
	{
		name: "deleted",
		re:   regexp.MustCompile(`(?i)\b(deleted|removed|supprimé|supprime)\b`),
		ok: func(calls []VerifiedCall) bool {
			if hasTool(calls, "delete_file") {
				return true
			}
			return shellMatched(`(?i)\b(rm|unlink|shred)\b`)(calls)
		},
	},
}

// UnverifiedClaims returns claim labels in summary that no successful tool call supports.
func UnverifiedClaims(summary string, calls []VerifiedCall) []string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	var out []string
	for _, rule := range finishClaimRules {
		if !rule.re.MatchString(summary) {
			continue
		}
		if rule.ok(calls) {
			continue
		}
		out = append(out, rule.name)
	}
	return out
}

func shellMatched(pattern string) func([]VerifiedCall) bool {
	re := regexp.MustCompile(pattern)
	return func(calls []VerifiedCall) bool {
		for _, c := range calls {
			if c.Tool != "run_shell" {
				continue
			}
			cmd := shellCmd(c.Args)
			if re.MatchString(cmd) || re.MatchString(c.Args) {
				return true
			}
		}
		return false
	}
}

func hasTool(calls []VerifiedCall, names ...string) bool {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, c := range calls {
		if want[c.Tool] {
			return true
		}
	}
	return false
}

func shellCmd(args string) string {
	var raw struct {
		Cmd     string `json:"cmd"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return args
	}
	if raw.Cmd != "" {
		return raw.Cmd
	}
	return raw.Command
}
