package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/redact"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func FormatTokens(n int) string {
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func formatTok(n int) string {
	if n >= 1000 {
		return FormatTokens(n)
	}
	return strconv.Itoa(n)
}

func ProgressLabel(base string, p llm.Progress) string {
	if p.ContentTokens > 0 {
		return "writing · " + formatTok(p.ContentTokens) + " tok"
	}
	if p.ReasoningTokens > 0 {
		return "thinking · " + formatTok(p.ReasoningTokens) + " tok"
	}
	return base
}

func FormatSummary(intent string, s Stats) string {
	total := s.ModelTime + s.ToolTime
	var parts []string
	if intent != "answer" && intent != "lookup" && s.Steps > 0 {
		if s.Steps == 1 {
			parts = append(parts, "1 step")
		} else {
			parts = append(parts, fmt.Sprintf("%d steps", s.Steps))
		}
	}
	if intent == "build" || intent == "act" {
		var inner []string
		if s.ModelTime > 0 {
			inner = append(inner, "model "+formatDuration(s.ModelTime))
		}
		if s.ToolTime > 0 {
			inner = append(inner, "tools "+formatDuration(s.ToolTime))
		}
		dur := formatDuration(total)
		if len(inner) > 0 {
			dur += " (" + strings.Join(inner, ", ") + ")"
		}
		if total > 0 || len(inner) > 0 {
			parts = append(parts, dur)
		}
	} else if total > 0 {
		parts = append(parts, formatDuration(total))
	}
	if s.PromptTokens > 0 || s.CompletionTokens > 0 {
		tok := formatTok(s.PromptTokens) + " → " + formatTok(s.CompletionTokens) + " tok"
		if s.ReasoningTokens > 0 {
			tok += " · think " + formatTok(s.ReasoningTokens)
		}
		parts = append(parts, tok)
	}
	if intent == "answer" && s.CompletionTokens > 0 && s.ModelTime > 0 {
		secs := s.ModelTime.Seconds()
		if secs > 0 {
			rate := int(float64(s.CompletionTokens)/secs + 0.5)
			parts = append(parts, fmt.Sprintf("%d tok/s", rate))
		}
	}
	return strings.Join(parts, " · ")
}

func formatArgSize(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%.1fKB", float64(n)/1000)
}

func FormatTraceCalls(calls []llm.ToolCall) []string {
	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = fmt.Sprintf("%s(args %s)", c.Function.Name, formatArgSize(len(c.Function.Arguments)))
	}
	return names
}

func FormatTrace(step int, think bool, d time.Duration, in, out, thinkTok, window int, names []string) string {
	th := "off"
	if think {
		th = "on"
	}
	pct := 0
	if window > 0 {
		pct = int(float64(in)*100/float64(window) + 0.5)
	}
	outField := FormatTokens(out)
	if thinkTok > 0 {
		outField += " think=" + FormatTokens(thinkTok)
	}
	return fmt.Sprintf("  [step %d] think=%s  %s  in=%s out=%s  ctx %d%% (%s/%s)  -> %s",
		step, th, formatDuration(d), FormatTokens(in), outField,
		pct, FormatTokens(in), FormatTokens(window), strings.Join(names, ", "))
}

func truncateNarration(s string) string {
	if utf8.RuneCountInString(s) <= 500 {
		return s
	}
	n := 0
	for i := range s {
		if n == 300 {
			return s[:i]
		}
		n++
	}
	return s
}

func stateHome() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".local", "state")
}

func dumpTrace(session string, step int, msg llm.Message) {
	if session == "" {
		session = "session"
	}
	dir := filepath.Join(stateHome(), "konsolai", "trace")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	calls := make([]llm.ToolCall, len(msg.ToolCalls))
	copy(calls, msg.ToolCalls)
	for i := range calls {
		calls[i].Function.Arguments = string(redact.JSON([]byte(calls[i].Function.Arguments)))
	}
	payload := struct {
		Content   string         `json:"content"`
		ToolCalls []llm.ToolCall `json:"tool_calls"`
	}{Content: redact.String(msg.Content), ToolCalls: calls}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", session, step))
	_ = os.WriteFile(path, b, 0600)
}

func PrintSummary(w io.Writer, intent string, s Stats) {
	if w == nil {
		return
	}
	line := FormatSummary(intent, s)
	if line == "" {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    "+ui.Current().Muted(line))
}

func FormatStatsLine(s Stats) string {
	return fmt.Sprintf("stats: steps=%d rejects=%d outside_plan=%d think_tokens=%d max_step_s=%s",
		s.Steps, s.Rejects, s.OutsidePlan, s.ReasoningTokens,
		strconv.FormatFloat(s.MaxStep.Seconds(), 'f', 3, 64))
}

func PrintTrace(w io.Writer, step int, think bool, d time.Duration, in, out, thinkTok, window int, names []string) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, FormatTrace(step, think, d, in, out, thinkTok, window, names))
}

func ShortModel(id string) string {
	if id == "" {
		return "-"
	}
	if i := strings.LastIndex(id, "/"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

func FormatRouter(model string, d time.Duration, effort string) string {
	return fmt.Sprintf("router %s %s · effort %s", ShortModel(model), formatDuration(d), effort)
}

func PrintRouter(w io.Writer, model string, d time.Duration, effort string) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, FormatRouter(model, d, effort))
}

func shortArgSummary(raw string) string {
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) == nil {
		for _, k := range []string{"path", "cmd", "summary"} {
			if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	s := strings.TrimSpace(raw)
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60])
	}
	return s
}

func printReject(w io.Writer, name, args, reason string) {
	if w == nil {
		return
	}
	ui.BeforeOutput()
	callLine, rejectLine := ui.RejectCall(name, shortArgSummary(args), reason)
	fmt.Fprintln(w, ui.CallIndent+callLine)
	fmt.Fprintln(w, ui.OutIndent+rejectLine)
}

func longerText(a, b string) string {
	a = trimLeadingBlanks(a)
	b = trimLeadingBlanks(b)
	if utf8.RuneCountInString(a) > utf8.RuneCountInString(b) {
		return a
	}
	return b
}

func writeAnswer(w io.Writer, text string, plain bool) {
	ui.BeforeOutput()
	text = ui.CollapseBlankLines(text)
	if plain {
		fmt.Fprintln(w, text)
		return
	}
	r := ui.NewRenderer(ui.NewIndentWriter(w, 4), false)
	_, _ = r.Write([]byte(text))
	if !strings.HasSuffix(text, "\n") {
		_, _ = r.Write([]byte("\n"))
	}
	_ = r.Flush()
}

func toolPrintedOutput(name string, r tools.Result) bool {
	if name != "run_shell" {
		return false
	}
	s := r.Output
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:]) != ""
	}
	return false
}

func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func trimLeadingBlanks(s string) string {
	return strings.TrimLeft(s, "\r\n")
}

func displayMeta(r tools.Result) string {
	if r.Meta != "" {
		return r.Meta
	}
	s := strings.TrimSpace(r.Output)
	s = strings.TrimPrefix(s, "error: ")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
