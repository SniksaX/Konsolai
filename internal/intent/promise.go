package intent

import (
	"regexp"
	"strings"
)

var promiseRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bi'll take a look\b`),
	regexp.MustCompile(`(?i)\blet me\b`),
	regexp.MustCompile(`(?i)\bi will (check|create|look|read|run|open)\b`),
	regexp.MustCompile(`(?i)\bi'll (check|create|look|read|run)\b`),
	regexp.MustCompile(`(?i)\blet me proceed\b`),
	regexp.MustCompile(`(?i)\bje vais (regarder|créer|verifier|vérifier|lire|lancer)\b`),
	regexp.MustCompile(`(?i)\blaisse-moi\b`),
}

func IsPromise(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, re := range promiseRes {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func IsPromiseContinue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "continue", "vas-y", "go ahead", "ok do it", "fais-le":
		return true
	default:
		return false
	}
}

func EscalateIntent(text string, verbs, targets []string) Intent {
	if _, ok := ActionFloor(text, verbs, targets); ok {
		return Build
	}
	return Lookup
}
