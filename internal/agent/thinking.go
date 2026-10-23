package agent

import (
	"strings"

	"github.com/SniksaX/Konsolai/internal/llm"
)

type ThinkingPolicy int

const (
	ThinkOff ThinkingPolicy = iota
	ThinkOn
	ThinkUntilPlan
	ThinkPlanStep
	ThinkLookup
)

func ParseBuildThinking(s string) ThinkingPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "until_plan":
		return ThinkUntilPlan
	default:
		return ThinkPlanStep
	}
}

func (cfg Config) thinkingBudget() int {
	if cfg.ThinkingMaxTokens > 0 {
		return cfg.ThinkingMaxTokens
	}
	return 4096
}

func applyThinkBudget(opts *llm.ChatOptions, budget int) {
	if opts == nil {
		return
	}
	if opts.Thinking && budget > 0 {
		opts.ThinkingBudget = budget
		return
	}
	opts.ThinkingBudget = 0
}

// planThinkAlways reports whether a valid propose_plan on a think=off step is discarded and rerun with thinking.
func planThinkAlways(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "always")
}

func (p ThinkingPolicy) enabled(planApproved, shellFail bool, consecFail int) bool {
	recover := shellFail || consecFail >= 2
	switch p {
	case ThinkOn:
		return true
	case ThinkUntilPlan:
		if !planApproved {
			return true
		}
		return recover
	case ThinkPlanStep:
		if !planApproved {
			return false
		}
		return recover
	case ThinkLookup:
		return consecFail >= 2
	default:
		return false
	}
}
