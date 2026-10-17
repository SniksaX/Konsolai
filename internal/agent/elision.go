package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

type ElideStats struct {
	Count  int
	Before int
	After  int
}

func ctxPct(n, window int) int {
	if window <= 0 {
		return 0
	}
	return int(float64(n)*100/float64(window) + 0.5)
}

func FormatElide(s ElideStats, window int) string {
	if s.Count == 0 {
		return ""
	}
	return fmt.Sprintf("  elided %d tool outputs (ctx %d%% -> %d%%)",
		s.Count, ctxPct(s.Before, window), ctxPct(s.After, window))
}

func (cfg Config) elideTrigger() float64 {
	if cfg.ElideTrigger > 0 {
		return cfg.ElideTrigger
	}
	return contextTrigger
}

func (cfg Config) elideTarget() float64 {
	if cfg.ElideTarget > 0 {
		return cfg.ElideTarget
	}
	return contextTarget
}

func shouldElide(estimate, window, cap int, trigger float64) bool {
	if window <= 0 {
		return false
	}
	if trigger <= 0 {
		trigger = contextTrigger
	}
	if float64(estimate) > trigger*float64(window) {
		return true
	}
	return window-estimate-contextSafety < cap/2
}

func isElided(s string) bool {
	return strings.HasPrefix(s, elidedPrefix) || strings.HasPrefix(s, elidedReadPrefix)
}

func isPlanTool(msgs []llm.Message, i int) bool {
	id := msgs[i].ToolCallID
	if id == "" {
		return false
	}
	for j := i - 1; j >= 0; j-- {
		if msgs[j].Role != llm.RoleAssistant {
			continue
		}
		for _, c := range msgs[j].ToolCalls {
			if c.ID == id && c.Function.Name == "propose_plan" {
				return true
			}
		}
	}
	return false
}

func toolCallOf(msgs []llm.Message, i int) (name, args string) {
	id := msgs[i].ToolCallID
	if id == "" {
		return "tool", ""
	}
	for j := i - 1; j >= 0; j-- {
		if msgs[j].Role != llm.RoleAssistant {
			continue
		}
		for _, c := range msgs[j].ToolCalls {
			if c.ID == id {
				n := c.Function.Name
				if n == "" {
					n = "tool"
				}
				return n, c.Function.Arguments
			}
		}
	}
	return "tool", ""
}

func toolNameOf(msgs []llm.Message, i int) string {
	n, _ := toolCallOf(msgs, i)
	return n
}

func pathFromArgs(raw string) string {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(raw), &a)
	return a.Path
}

func dropSpanByID(spans []readSpan, id string) []readSpan {
	if id == "" {
		return spans
	}
	out := spans[:0]
	for _, sp := range spans {
		if sp.toolID != id {
			out = append(out, sp)
		}
	}
	return out
}

func spanVisible(sp readSpan, msgs []llm.Message) bool {
	if sp.toolID == "" {
		return false
	}
	for _, m := range msgs {
		if m.Role != llm.RoleTool || m.ToolCallID != sp.toolID {
			continue
		}
		if isElided(m.Content) || strings.HasPrefix(m.Content, "already in context") {
			return false
		}
		return true
	}
	return false
}

func capElideOutline(s string) string {
	if len(s) <= elideOutlineCap {
		return s
	}
	s = s[:elideOutlineCap]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func fileElidePlaceholder(abs, rel string) (string, bool) {
	if abs == "" {
		return "", false
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return "", false
	}
	if rel == "" {
		rel = filepath.Base(abs)
	}
	n := fileLineCount(abs)
	outline := capElideOutline(tools.FileOutline(abs, rel))
	return fmt.Sprintf("[elided] %s (%d lines). Outline:\n%s\nRe-read only the specific line ranges you need.", rel, n, outline), true
}

func elideSummary(content string) string {
	s := strings.TrimSpace(content)
	s = strings.TrimPrefix(s, "error: ")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = "output"
	}
	r := []rune(s)
	if len(r) > elideSummaryMax {
		s = string(r[:elideSummaryMax])
	}
	return s
}

func elidedMarker(tool, summary string) string {
	return fmt.Sprintf("[tool output elided: %s %s]", tool, summary)
}

type readSpan struct {
	abs    string
	hash   string
	start  int
	end    int
	step   int
	toolID string
}

func parseReadRange(raw string) (start, end int) {
	var a struct {
		StartLine int `json:"start_line"`
		EndLine   int `json:"end_line"`
	}
	_ = json.Unmarshal([]byte(raw), &a)
	start = a.StartLine
	if start < 1 {
		start = 1
	}
	return start, a.EndLine
}

func parseReadHeader(out string) (start, end int, ok bool) {
	line, _, _ := strings.Cut(out, "\n")
	i := strings.LastIndex(line, "(lines ")
	if i < 0 {
		return 0, 0, false
	}
	rest := strings.TrimSuffix(strings.TrimSpace(line[i+7:]), ")")
	rest, _, _ = strings.Cut(rest, " of ")
	var a, b int
	if _, err := fmt.Sscanf(rest, "%d-%d", &a, &b); err != nil {
		return 0, 0, false
	}
	return a, b, true
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}

func fileLineCount(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	if len(b) == 0 {
		return 0
	}
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

func (s readSpan) covers(start, end int) bool {
	if s.start > start {
		return false
	}
	return s.end >= end
}

func findReadSpan(spans []readSpan, abs, hash string, start, end int) (readSpan, bool) {
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		if sp.abs == abs && sp.hash == hash && sp.covers(start, end) {
			return sp, true
		}
	}
	return readSpan{}, false
}

func purgeElidedReads(spans []readSpan, msgs []llm.Message) []readSpan {
	dead := map[string]bool{}
	for _, m := range msgs {
		if m.Role == llm.RoleTool && isElided(m.Content) && m.ToolCallID != "" {
			dead[m.ToolCallID] = true
		}
	}
	if len(dead) == 0 {
		return spans
	}
	out := spans[:0]
	for _, sp := range spans {
		if !dead[sp.toolID] {
			out = append(out, sp)
		}
	}
	return out
}

func elideOneTool(msgs []llm.Message, spans *[]readSpan, seen map[string]struct{}) bool {
	limit := len(msgs) - protectLastMsgs
	if limit <= 0 {
		return false
	}
	for i := 0; i < limit; i++ {
		if msgs[i].Role != llm.RoleTool || isElided(msgs[i].Content) {
			continue
		}
		if isPlanTool(msgs, i) {
			continue
		}
		name, args := toolCallOf(msgs, i)
		rel := pathFromArgs(args)
		abs := rel
		id := msgs[i].ToolCallID
		if spans != nil {
			for _, sp := range *spans {
				if sp.toolID == id && sp.abs != "" {
					abs = sp.abs
					break
				}
			}
		}
		if !filepath.IsAbs(abs) && abs != "" {
			if a, err := filepath.Abs(abs); err == nil {
				abs = a
			}
		}
		replaced := false
		if name == "read_file" || name == "outline" {
			if ph, ok := fileElidePlaceholder(abs, rel); ok {
				msgs[i].Content = ph
				replaced = true
			}
		}
		if !replaced {
			if name == "" {
				name = "tool"
			}
			msgs[i].Content = elidedMarker(name, elideSummary(msgs[i].Content))
		}
		if spans != nil {
			*spans = dropSpanByID(*spans, id)
		}
		if seen != nil && name != "" && args != "" {
			delete(seen, name+"\x00"+canonicalJSON(args))
		}
		return true
	}
	return false
}

func lastUserTask(msgs []llm.Message, task string) int {
	idx := -1
	for i, m := range msgs {
		if m.Role == llm.RoleUser && m.Content == task {
			idx = i
		}
	}
	return idx
}

func aggressiveTrim(msgs []llm.Message, userIdx int) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	keep := make([]bool, len(msgs))
	if msgs[0].Role == llm.RoleSystem {
		keep[0] = true
	}
	if userIdx >= 0 && userIdx < len(msgs) {
		keep[userIdx] = true
	}
	for i, m := range msgs {
		if m.Role != llm.RoleTool || !isPlanTool(msgs, i) {
			continue
		}
		keep[i] = true
		for j := i - 1; j >= 0; j-- {
			if msgs[j].Role == llm.RoleAssistant {
				keep[j] = true
				break
			}
		}
	}
	start := len(msgs) - 4
	if start < 0 {
		start = 0
	}
	for i := start; i < len(msgs); i++ {
		keep[i] = true
	}
	out := make([]llm.Message, 0, len(msgs))
	for i, m := range msgs {
		if !keep[i] {
			continue
		}
		if m.Role == llm.RoleTool && len(m.Content) > 400 {
			m.Content = m.Content[:400]
		}
		out = append(out, m)
	}
	return out
}

func (cfg Config) prepCall(msgs []llm.Message, tools []llm.ToolSpec, spans *[]readSpan, seen map[string]struct{}) (estimate, maxTokens int, elide ElideStats, err error) {
	return prepareCall(cfg.Client, msgs, tools, cfg.Intent, spans, seen, cfg.tokenCap(), cfg.elideTrigger(), cfg.elideTarget())
}

func PrepareCall(client *llm.Client, msgs []llm.Message, tools []llm.ToolSpec, intent string) (estimate, maxTokens int, elide ElideStats, err error) {
	return prepareCall(client, msgs, tools, intent, nil, nil, 0, 0, 0)
}

func prepareCall(client *llm.Client, msgs []llm.Message, tools []llm.ToolSpec, intent string, spans *[]readSpan, seen map[string]struct{}, overrideCap int, trigger, target float64) (estimate, maxTokens int, elide ElideStats, err error) {
	window := client.ContextWindow()
	cap := overrideCap
	if cap <= 0 {
		cap = ProfileCap(intent)
	}
	if trigger <= 0 {
		trigger = contextTrigger
	}
	if target <= 0 {
		target = contextTarget
	}
	estimate = client.EstimatePromptTokens(msgs, tools)
	if shouldElide(estimate, window, cap, trigger) {
		elide.Before = estimate
		for {
			est := client.EstimatePromptTokens(msgs, tools)
			if float64(est) <= target*float64(window) {
				break
			}
			if !elideOneTool(msgs, spans, seen) {
				break
			}
			elide.Count++
		}
		estimate = client.EstimatePromptTokens(msgs, tools)
		elide.After = estimate
	}
	if window-estimate-contextSafety < minOutputTokens {
		return estimate, 0, elide, ErrContextOverflow
	}
	return estimate, MaxTokensFor(window, estimate, cap), elide, nil
}

func skipCoveredRead(action tools.Action, call llm.ToolCall, spans []readSpan, msgs []llm.Message, out io.Writer) (bool, llm.Message) {
	type hasResolved interface{ ResolvedPath() string }
	rp, ok := action.(hasResolved)
	if !ok {
		return false, llm.Message{}
	}
	abs := rp.ResolvedPath()
	hash, err := fileSHA256(abs)
	if err != nil {
		return false, llm.Message{}
	}
	start, end := parseReadRange(call.Function.Arguments)
	if end < 1 {
		end = fileLineCount(abs)
	}
	sp, ok := findReadSpan(spans, abs, hash, start, end)
	if !ok || !spanVisible(sp, msgs) {
		return false, llm.Message{}
	}
	summary := action.Summary()
	if out != nil {
		ui.BeforeOutput()
		fmt.Fprintln(out, ui.CallIndent+ui.FormatToolSummary(summary+" (already shown, skipped)"))
	}
	return true, llm.Message{
		Role:       llm.RoleTool,
		Content:    fmt.Sprintf("already in context above (step %d, lines %d-%d)", sp.step, sp.start, sp.end),
		ToolCallID: call.ID,
	}
}

const (
	resumeTokenCap   = 6000
	resumeArgMax     = 200
	resumeToolElided = "[tool output from an earlier turn elided]"
)

func LeanResume(hist []llm.Message, tokensPerChar float64) []llm.Message {
	if len(hist) == 0 {
		return hist
	}
	if tokensPerChar <= 0 {
		tokensPerChar = 0.30
	}
	lastUser := -1
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == llm.RoleUser {
			lastUser = i
			break
		}
	}
	out := make([]llm.Message, len(hist))
	copy(out, hist)
	for i := range out {
		if n := len(out[i].ToolCalls); n > 0 {
			tcs := make([]llm.ToolCall, n)
			copy(tcs, out[i].ToolCalls)
			out[i].ToolCalls = tcs
		}
	}
	for i := range out {
		if out[i].Role == llm.RoleAssistant {
			for j := range out[i].ToolCalls {
				args := out[i].ToolCalls[j].Function.Arguments
				if len(args) > resumeArgMax {
					out[i].ToolCalls[j].Function.Arguments = args[:resumeArgMax]
				}
			}
		}
		if i < lastUser && out[i].Role == llm.RoleTool {
			out[i].Content = resumeToolElided
		}
	}
	for resumeTokens(out, tokensPerChar) > resumeTokenCap && canDropPair(out) {
		out = dropOldestPair(out)
	}
	return out
}

func resumeTokens(msgs []llm.Message, ratio float64) int {
	return int(float64(llm.MessageChars(msgs)) * ratio)
}

func canDropPair(msgs []llm.Message) bool {
	users := 0
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			users++
		}
	}
	if users >= 2 {
		return true
	}
	return len(msgs) > 0 && msgs[0].Role != llm.RoleUser && users >= 1
}

func dropOldestPair(msgs []llm.Message) []llm.Message {
	if len(msgs) == 0 {
		return msgs
	}
	if msgs[0].Role != llm.RoleUser {
		i := 0
		for i < len(msgs) && msgs[i].Role != llm.RoleUser {
			i++
		}
		if i > 0 {
			return msgs[i:]
		}
	}
	i := 1
	for i < len(msgs) && msgs[i].Role != llm.RoleUser {
		i++
	}
	return msgs[i:]
}
