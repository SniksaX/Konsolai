package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/tools"
)

func hasProposePlan(msg llm.Message) bool {
	for _, c := range msg.ToolCalls {
		if c.Function.Name == "propose_plan" {
			return true
		}
	}
	return false
}

func hasFinishCall(msg llm.Message) bool {
	for _, c := range msg.ToolCalls {
		if c.Function.Name == "finish" {
			return true
		}
	}
	return false
}

func proposePlanValid(reg *tools.Registry, msg llm.Message) bool {
	if reg == nil {
		return true
	}
	tool, ok := reg.Get("propose_plan")
	if !ok {
		return false
	}
	for _, c := range msg.ToolCalls {
		if c.Function.Name != "propose_plan" {
			continue
		}
		if _, err := tool.Prepare(c.Function.Arguments); err != nil {
			return false
		}
	}
	return true
}

func mutatingSpecs(specs []llm.ToolSpec) []llm.ToolSpec {
	keep := map[string]bool{
		"write_file": true, "edit_file": true, "run_shell": true,
		"export_doc": true, "remember": true, "finish": true,
	}
	out := make([]llm.ToolSpec, 0, 6)
	for _, s := range specs {
		if keep[s.Function.Name] {
			out = append(out, s)
		}
	}
	return out
}

func exportOnlySpecs(specs []llm.ToolSpec) []llm.ToolSpec {
	out := make([]llm.ToolSpec, 0, 2)
	for _, s := range specs {
		if s.Function.Name == "export_doc" || s.Function.Name == "finish" {
			out = append(out, s)
		}
	}
	return out
}

func docsPlannedWriteNote(files []string) string {
	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f), ".md") {
			return "Write " + f + " with write_file now."
		}
	}
	return "Write the planned Markdown file with write_file now."
}

func planOnlySpecs(specs []llm.ToolSpec) []llm.ToolSpec {
	for _, s := range specs {
		if s.Function.Name == "propose_plan" {
			return []llm.ToolSpec{s}
		}
	}
	return nil
}

func canonicalJSON(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(b)
}

func filePathOf(a tools.Action) (string, bool) {
	type hasPath interface{ FilePath() string }
	p, ok := a.(hasPath)
	if !ok {
		return "", false
	}
	return p.FilePath(), true
}

func wantsDocExport(request string, files []string) bool {
	if strings.Contains(strings.ToLower(request), ".pdf") || strings.Contains(strings.ToLower(request), ".html") {
		return true
	}
	for _, f := range files {
		fl := strings.ToLower(f)
		if strings.Contains(fl, ".pdf") || strings.Contains(fl, ".html") {
			return true
		}
	}
	return false
}

func planFilesOf(a tools.Action) []string {
	type hasFiles interface{ PlanFiles() []string }
	p, ok := a.(hasFiles)
	if !ok {
		return nil
	}
	return p.PlanFiles()
}

func planCmdsOf(a tools.Action) []string {
	type hasCmds interface{ PlanCommands() []string }
	p, ok := a.(hasCmds)
	if !ok {
		return nil
	}
	return p.PlanCommands()
}

func RestoreApprovedPlan(msgs []llm.Message) (files, cmds []string, text string, ok bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Function.Name != "propose_plan" {
				continue
			}
			approved := false
			for j := i + 1; j < len(msgs); j++ {
				if msgs[j].Role == llm.RoleTool && msgs[j].ToolCallID == tc.ID && strings.Contains(msgs[j].Content, "plan approved") {
					approved = true
					break
				}
				if msgs[j].Role == llm.RoleAssistant {
					break
				}
			}
			if !approved {
				continue
			}
			var args struct {
				Summary  string   `json:"summary"`
				Steps    []string `json:"steps"`
				Files    []string `json:"files"`
				Commands []string `json:"commands"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				continue
			}
			text = args.Summary + "\n" + strings.Join(args.Steps, "\n") + "\n" + strings.Join(args.Files, "\n") + "\n" + strings.Join(args.Commands, "\n")
			return args.Files, args.Commands, text, true
		}
	}
	return nil, nil, "", false
}

func planTextOf(a tools.Action) string {
	type hasText interface{ PlanText() string }
	if p, ok := a.(hasText); ok {
		return p.PlanText()
	}
	return a.Summary() + "\n" + a.Preview()
}

func commandOf(a tools.Action) (string, bool) {
	type hasCmd interface{ PlanCommand() string }
	p, ok := a.(hasCmd)
	if !ok {
		return "", false
	}
	return p.PlanCommand(), true
}

func inPlanCmd(cmds []string, cmd string) bool {
	ok, _ := safety.MatchPlan(safety.Plan{Commands: cmds}, cmd)
	return ok
}

func inPlan(files []string, path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(path), "./")
	for _, f := range files {
		if strings.TrimPrefix(filepath.ToSlash(f), "./") == path {
			return true
		}
	}
	return false
}

func identicalRejectLimit(cfg Config) int {
	if cfg.IdenticalRejects > 0 {
		return cfg.IdenticalRejects
	}
	return 2
}

func toolProtocolValid(msgs []llm.Message) bool {
	pending := map[string]bool{}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleAssistant:
			pending = map[string]bool{}
			for _, c := range m.ToolCalls {
				if c.ID != "" {
					pending[c.ID] = true
				}
			}
		case llm.RoleTool:
			if m.ToolCallID == "" || !pending[m.ToolCallID] {
				return false
			}
		}
	}
	return true
}
