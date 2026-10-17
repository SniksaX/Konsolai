package agent

import (
	"strings"
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
