package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

func ExploreBudgetFor(intent, task string) int {
	switch intent {
	case "lookup":
		if task == "analysis" {
			return 10
		}
		return 6
	case "build":
		switch task {
		case "docs":
			return 16
		case "analysis":
			return 12
		}
		return 8
	default:
		return 6
	}
}

func ProfileCap(intent string) int {
	if intent == "build" {
		return 12288
	}
	return 4096
}

func MaxTokensFor(window, estimate, cap int) int {
	room := window - estimate - contextSafety
	if room < 1 {
		return 0
	}
	if room < cap {
		return room
	}
	return cap
}

func (cfg Config) tokenCap() int {
	if cfg.TokenCap > 0 {
		return cfg.TokenCap
	}
	return ProfileCap(cfg.Intent)
}

func analysisLimit(cfg Config, sourceReads int) int {
	n := cfg.MaxSteps
	if n < 1 {
		n = 15
	}
	if cfg.Task == "analysis" && sourceReads < 3 {
		return n + 6
	}
	return n
}

func hardStopAfter(cfg Config, sourceReads int) int {
	if cfg.Task == "analysis" && sourceReads < 3 {
		return 8
	}
	return 2
}

func exploreNote(cfg Config, sourceReads int) string {
	if cfg.Task == "analysis" && sourceReads < 3 {
		return AnalysisExploreNote
	}
	return cfg.ExploreNote
}

func analysisSourceRead(name, args string) bool {
	if name != "read_file" && name != "outline" {
		return false
	}
	var p struct {
		Path string `json:"path"`
	}
	if json.Unmarshal([]byte(args), &p) != nil {
		return false
	}
	base := strings.ToLower(filepath.Base(p.Path))
	switch base {
	case "package.json", "go.mod", "go.sum", "pyproject.toml", "requirements.txt", "cargo.toml", "cargo.lock":
		return false
	}
	if strings.HasPrefix(base, "readme") {
		return false
	}
	return p.Path != ""
}
