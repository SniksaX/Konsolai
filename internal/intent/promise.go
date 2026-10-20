package intent

import (
	"regexp"
	"strings"
)

var promiseRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bi'll take a look\b`),
	regexp.MustCompile(`(?i)\blet me\b`),
	regexp.MustCompile(`(?i)\bi will (check|create|look|read|run|open|kill|stop|start|delete|test)\b`),
	regexp.MustCompile(`(?i)\bi'll (check|create|look|read|run|kill|stop|start|delete|test)\b`),
	regexp.MustCompile(`(?i)\blet me proceed\b`),
	regexp.MustCompile(`(?i)\bje vais (regarder|créer|creer|verifier|vérifier|lire|lancer|tuer|supprimer|arrêter|arreter|tester)`),
	regexp.MustCompile(`(?i)\blaisse-moi\b`),
	regexp.MustCompile(`(?i)\bwait\.\.\.`),
}

var claimRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bdone\b`),
	regexp.MustCompile(`(?i)\bkilled\b`),
	regexp.MustCompile(`(?i)\bterminated\b`),
	regexp.MustCompile(`(?i)\bstopped\b`),
	regexp.MustCompile(`(?i)\bstarted\b`),
	regexp.MustCompile(`(?i)\bis running\b`),
	regexp.MustCompile(`(?i)\bis now free\b`),
	regexp.MustCompile(`(?i)\bi('ve| have)? ran\b`),
	regexp.MustCompile(`(?i)\bi ran\b`),
	regexp.MustCompile(`(?i)\bi('ve| have)? run\b`),
	regexp.MustCompile(`(?i)\bi('ve| have)? tested\b`),
	regexp.MustCompile(`(?i)\bi tested\b`),
	regexp.MustCompile(`(?i)\bi('ve| have)? created\b`),
	regexp.MustCompile(`(?i)\bi created\b`),
	regexp.MustCompile(`(?i)\bi('ve| have)? deleted\b`),
	regexp.MustCompile(`(?i)\bi deleted\b`),
	regexp.MustCompile(`(?i)\bi('ve| have)? installed\b`),
	regexp.MustCompile(`(?i)\bi installed\b`),
	regexp.MustCompile(`(?i)\[running`),
	regexp.MustCompile(`(?i)\brunning test\b`),
	regexp.MustCompile(`(?i)c'est fait`),
	regexp.MustCompile(`(?i)j'ai lancé`),
	regexp.MustCompile(`(?i)j'ai (supprimé|supprime)`),
	regexp.MustCompile(`(?i)j'ai (créé|cree)`),
	regexp.MustCompile(`(?i)processus tué`),
	regexp.MustCompile(`(?i)\bprocess killed\b`),
	regexp.MustCompile(`(?i)\bport \d+ is now free\b`),
}

var (
	reFence   = regexp.MustCompile("(?s)```.*?```")
	reInline  = regexp.MustCompile("`[^`]*`")
	reDQuoted = regexp.MustCompile(`"[^"\n]{1,200}"`)
	reSQuoted = regexp.MustCompile(`'[^'\n]{1,200}'`)
)

// IsPromise reports a future-tense promise to act.
func IsPromise(s string) bool {
	return matchAny(stripProtected(s), promiseRes)
}

// ClaimsAction reports that the reply claims an action was performed, or promises one.
func ClaimsAction(s string) bool {
	plain := stripProtected(s)
	if matchAny(plain, promiseRes) || matchAny(plain, claimRes) {
		return true
	}
	return false
}

func stripProtected(s string) string {
	s = reFence.ReplaceAllString(s, " ")
	s = reInline.ReplaceAllString(s, " ")
	s = reDQuoted.ReplaceAllString(s, " ")
	s = reSQuoted.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func matchAny(s string, list []*regexp.Regexp) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, re := range list {
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

var contradictRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\byou didn't\b`),
	regexp.MustCompile(`(?i)\byou did not\b`),
	regexp.MustCompile(`(?i)\bit'?s still running\b`),
	regexp.MustCompile(`(?i)\bstill (running|there|alive)\b`),
	regexp.MustCompile(`(?i)ça marche pas`),
	regexp.MustCompile(`(?i)ca marche pas`),
	regexp.MustCompile(`(?i)t'as rien fait`),
	regexp.MustCompile(`(?i)tu n'as rien fait`),
	regexp.MustCompile(`(?i)\bnothing happened\b`),
	regexp.MustCompile(`(?i)\bstill listening\b`),
}

// IsContradiction reports a short reply that disputes a claimed action.
func IsContradiction(s string) bool {
	return matchAny(stripProtected(s), contradictRes)
}

// EscalateIntent picks lookup for state questions and the action floor otherwise.
func EscalateIntent(text string, verbs, targets []string) Intent {
	if floor, ok := ActionFloor(text, verbs, targets); ok {
		return floor
	}
	if isStateQuestion(text) {
		return Lookup
	}
	return Act
}

func isStateQuestion(text string) bool {
	trim := strings.TrimSpace(text)
	if trim == "" {
		return false
	}
	folded := fold(trim)
	first := firstWord(folded)
	for _, p := range howPrefixes {
		if first == p {
			return true
		}
	}
	switch first {
	case "is", "are", "was", "were", "what", "who", "where", "which", "does", "do", "did",
		"est", "sont", "quel", "quelle", "quels", "quelles", "y":
		return true
	}
	return strings.HasSuffix(trim, "?") && !hasWord(folded, defaultVerbs)
}
