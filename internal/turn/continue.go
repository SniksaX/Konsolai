package turn

import (
	"strings"

	"github.com/siraj/konsolai/internal/intent"
)

const continueNote = "The previous attempt stopped before finishing; continue from where it stopped."

func isContinue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "continue", "go on", "keep going", "vas-y", "continue stp", "poursuis", "go ahead", "ok do it", "fais-le":
		return true
	default:
		return false
	}
}

func incompleteTurn(code int) bool {
	return code == 1 || code == 3 || code == 4
}

func (r *Runner) continueDecision(text string) (intent.Decision, bool) {
	if r == nil {
		return intent.Decision{}, false
	}
	if intent.IsPromiseContinue(text) && r.lastPromise && r.lastUser != "" && r.lastEscalate != "" {
		return intent.Decision{
			Intent: r.lastEscalate, Language: r.lastLang, Reason: "continue-promise",
			Effort: intent.EffortHigh, Task: r.lastTask,
		}, true
	}
	if !isContinue(text) || !incompleteTurn(r.lastCode) || r.lastIntent == "" {
		if intent.HoldIntent(r.lastIntent, r.lastAssistant, text) {
			return intent.Decision{
				Intent: r.lastIntent, Language: r.lastLang, Reason: "clarification",
				Effort: r.lastEffort, Task: r.lastTask,
			}, true
		}
		return intent.Decision{}, false
	}
	return intent.Decision{
		Intent: r.lastIntent, Language: r.lastLang, Reason: "continue",
		Effort: r.lastEffort, Task: r.lastTask,
	}, true
}
