package llm

import "strings"

// Normalize prepares a chat history for a model request: merge consecutive
// user messages, drop empty assistants, start with a user after system, and
// keep tool results beside their assistant tool_calls.
func Normalize(msgs []Message) []Message {
	tmp := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		tmp = append(tmp, m)
	}
	merged := make([]Message, 0, len(tmp))
	for _, m := range tmp {
		if m.Role == RoleUser && len(merged) > 0 && merged[len(merged)-1].Role == RoleUser {
			prev := strings.TrimRight(merged[len(merged)-1].Content, "\n")
			next := strings.TrimLeft(m.Content, "\n")
			merged[len(merged)-1].Content = prev + "\n\n" + next
			continue
		}
		merged = append(merged, m)
	}
	kept := make([]Message, 0, len(merged))
	for _, m := range merged {
		if m.Role == RoleTool {
			if !toolFollowsAssistant(kept, m) {
				continue
			}
		}
		kept = append(kept, m)
	}
	i := 0
	if i < len(kept) && kept[i].Role == RoleSystem {
		i++
	}
	j := i
	for j < len(kept) && kept[j].Role != RoleUser {
		if kept[j].Role == RoleAssistant {
			j++
			for j < len(kept) && kept[j].Role == RoleTool {
				j++
			}
			continue
		}
		j++
	}
	if i == 0 {
		return kept[j:]
	}
	out := make([]Message, 0, 1+len(kept)-j)
	out = append(out, kept[:i]...)
	out = append(out, kept[j:]...)
	return out
}

func toolFollowsAssistant(kept []Message, m Message) bool {
	if len(kept) == 0 {
		return false
	}
	last := kept[len(kept)-1]
	if last.Role == RoleTool {
		return true
	}
	if last.Role != RoleAssistant || len(last.ToolCalls) == 0 {
		return false
	}
	if m.ToolCallID == "" {
		return true
	}
	for _, tc := range last.ToolCalls {
		if tc.ID == m.ToolCallID {
			return true
		}
	}
	return false
}
