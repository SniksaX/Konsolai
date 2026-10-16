package turn

import (
	"strings"

	"github.com/siraj/konsolai/internal/intent"
)

const continueNote = "The previous attempt stopped before finishing; continue from where it stopped."

func isContinue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "continue", "go on", "keep going", "vas-y", "continue stp", "poursuis":
		return true
	default:
		return false
	}
}

func incompleteTurn(code int) bool {
	return code == 1 || code == 3 || code == 4
}

func (r *Runner) continueDecision(text string) (intent.Decision, bool) {
	if r == nil || !isContinue(text) || !incompleteTurn(r.lastCode) || r.lastIntent == "" {
		return intent.Decision{}, false
	}
	return intent.Decision{
		Intent: r.lastIntent, Language: r.lastLang, Reason: "continue",
		Effort: r.lastEffort, Task: r.lastTask,
	}, true
}
