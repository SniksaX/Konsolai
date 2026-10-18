package turn

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
)

const (
	compactKeep   = 4
	compactPrompt = "Summarize this conversation for continuing it: user goals, decisions, files changed, open issues. Under 300 words."
)

func ShouldCompact(promptTokens, window int) bool {
	if window < 1 || promptTokens < 1 {
		return false
	}
	return ShouldCompactRatio(promptTokens, window, 0)
}

func ShouldCompactRatio(promptTokens, window int, threshold float64) bool {
	if window < 1 || promptTokens < 1 {
		return false
	}
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.7
	}
	return float64(promptTokens) > threshold*float64(window)
}

func HistoryWithSummary(msgs []llm.Message, summary string, after int) []llm.Message {
	if strings.TrimSpace(summary) == "" {
		return msgs
	}
	rest := msgs
	if after > 0 {
		if after >= len(msgs) {
			rest = nil
		} else {
			rest = msgs[after:]
		}
	}
	note := "Summary of earlier conversation: " + summary
	if len(rest) > 0 && rest[0].Role == llm.RoleUser {
		rest[0].Content = note + "\n\n" + rest[0].Content
		return rest
	}
	return append([]llm.Message{{Role: llm.RoleUser, Content: note}}, rest...)
}

func LoadCompacted(ctx context.Context, mem *memory.Guard, sessionID, profile, cwd string) []llm.Message {
	if sessionID != "" && mem != nil && mem.Store() != nil {
		s := mem.Store()
		sess, err := s.SessionByID(ctx, sessionID)
		msgs, merr := s.LoadMessages(ctx, sessionID)
		if merr != nil {
			mem.Fail(merr)
			return nil
		}
		llmMsgs := memory.ToLLM(msgs)
		if err == nil && sess.CompactSummary != "" {
			var rest []store.Message
			upto := 0
			if sess.CompactedUpto != nil {
				upto = *sess.CompactedUpto
			}
			for _, m := range msgs {
				if m.Seq > upto {
					rest = append(rest, m)
				}
			}
			return HistoryWithSummary(memory.ToLLM(rest), sess.CompactSummary, 0)
		}
		return llmMsgs
	}
	if sess, ok := memory.LoadLast(profile, cwd, 0); ok {
		if sess.CompactSummary != "" && !hasSummaryNote(sess.Messages) {
			return HistoryWithSummary(sess.Messages, sess.CompactSummary, sess.CompactedUpto)
		}
		return sess.Messages
	}
	return nil
}

func hasSummaryNote(msgs []llm.Message) bool {
	return len(msgs) > 0 && strings.HasPrefix(msgs[0].Content, "Summary of earlier conversation:")
}

func CompactMessages(ctx context.Context, client *llm.Client, msgs []llm.Message) (summary string, upto int, err error) {
	if len(msgs) <= compactKeep {
		return "", 0, nil
	}
	head := msgs[:len(msgs)-compactKeep]
	var b strings.Builder
	for _, m := range head {
		if m.Role == llm.RoleSystem {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
	}
	out, _, err := client.Complete(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: compactPrompt},
		{Role: llm.RoleUser, Content: b.String()},
	}, nil, llm.ChatOptions{Thinking: false, MaxTokens: 1500, Temperature: 0.2})
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(out.Content), len(head), nil
}

func PersistCompact(ctx context.Context, mem *memory.Guard, sessionID, profile, cwd, summary string, upto int, msgs []llm.Message) {
	if mem != nil && mem.Store() != nil && sessionID != "" {
		uptoSeq := upto
		if rows, err := mem.Store().LoadMessages(ctx, sessionID); err == nil && len(rows) > compactKeep {
			uptoSeq = rows[len(rows)-compactKeep-1].Seq
		}
		if err := mem.Store().SetCompact(ctx, sessionID, uptoSeq, summary); err != nil {
			mem.Fail(err)
		}
	}
	prev, _ := memory.LoadLast(profile, cwd, 0)
	prev.Profile = profile
	prev.CWD = cwd
	prev.Messages = msgs
	prev.CompactSummary = summary
	prev.CompactedUpto = upto
	_ = memory.SaveLast(prev)
}

func PrintCompacted(w io.Writer, n, beforeTok, afterTok, window int) {
	bp, ap := 0, 0
	if window > 0 {
		bp = int(float64(beforeTok) / float64(window) * 100)
		ap = int(float64(afterTok) / float64(window) * 100)
	}
	fmt.Fprintln(w, ui.Current().Muted(fmt.Sprintf("compacted %d messages (ctx %d%% -> %d%%)", n, bp, ap)))
}
