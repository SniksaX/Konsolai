package llm

// MissingToolReply is the synthetic result sent for a tool call that never ran,
// for example because the turn was interrupted or the saved history lost it.
const MissingToolReply = "not executed: turn ended"

// RepairToolCalls makes a chat message list valid for OpenAI-compatible model
// servers: every assistant message with tool_calls is followed by exactly one
// tool reply per tool_call_id, in call order, before any other message. A reply
// that is missing becomes MissingToolReply; replies no assistant owns and
// duplicate replies are dropped. The repair never reorders a complete group.
func RepairToolCalls(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == RoleTool {
			// Only a reply that directly follows its assistant is kept; the
			// assistant branch below consumes and orders those replies.
			continue
		}
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			out = append(out, m)
			continue
		}
		replies := make(map[string]Message, len(m.ToolCalls))
		j := i + 1
		for ; j < len(msgs) && msgs[j].Role == RoleTool; j++ {
			id := msgs[j].ToolCallID
			if id == "" {
				continue
			}
			if _, dup := replies[id]; !dup {
				replies[id] = msgs[j]
			}
		}
		out = append(out, m)
		seen := make(map[string]struct{}, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			if _, dup := seen[tc.ID]; dup {
				continue
			}
			seen[tc.ID] = struct{}{}
			if reply, ok := replies[tc.ID]; ok {
				out = append(out, reply)
				continue
			}
			out = append(out, Message{Role: RoleTool, ToolCallID: tc.ID, Content: MissingToolReply})
		}
		i = j - 1
	}
	return out
}

// MissingToolReplies returns the synthetic replies a repaired list would add
// for the trailing assistant message, in tool_call order. It is used to make a
// recorded history valid after an interrupted turn.
func MissingToolReplies(msgs []Message) []Message {
	idx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleUser {
			break
		}
		if msgs[i].Role == RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	answered := map[string]bool{}
	for _, m := range msgs[idx+1:] {
		if m.Role == RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}
	var out []Message
	seen := map[string]bool{}
	for _, tc := range msgs[idx].ToolCalls {
		if tc.ID == "" || answered[tc.ID] || seen[tc.ID] {
			continue
		}
		seen[tc.ID] = true
		out = append(out, Message{Role: RoleTool, ToolCallID: tc.ID, Content: MissingToolReply})
	}
	return out
}
