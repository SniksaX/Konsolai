package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/redact"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

type Recorder interface {
	Message(ctx context.Context, m llm.Message) error
	Action(ctx context.Context, a ActionRecord) error
	FileChange(ctx context.Context, c tools.FileChange) error
}

type ActionRecord struct {
	Seq          int
	Tool         string
	Args         json.RawMessage
	Kind         tools.ActionKind
	Decision     string
	RejectReason string
	IsError      bool
	Meta         string
	Duration     time.Duration
	Output       string
}

type NopRecorder struct{}

func (NopRecorder) Message(context.Context, llm.Message) error         { return nil }
func (NopRecorder) Action(context.Context, ActionRecord) error         { return nil }
func (NopRecorder) FileChange(context.Context, tools.FileChange) error { return nil }

type Confirmer interface {
	Confirm(summary, preview string) (ui.Decision, string, error)
	ConfirmForced(summary, preview string) (ui.Decision, string, error)
}

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

type Config struct {
	Client         *llm.Client
	Tools          *tools.Registry
	Confirmer      Confirmer
	Out            io.Writer
	MaxSteps       int
	ThinkingPolicy ThinkingPolicy
	RequirePlan    bool
	Prompt         string
	Temperature    float64
	Recorder       Recorder
	History        []llm.Message
	AutoApprove    bool
	ExploreBudget  int
	ExploreNote    string
	Trace          bool
	SessionID      string
	Intent         string
	Task           string
	RouterTime     time.Duration
	Err            io.Writer
	Stats          *Stats
	Plain          bool
	OnShell        func(output string)
	Guard          *safety.Guard
	ResumePlan     bool
	PlanFiles      []string
	PlanCmds       []string
	PlanText       string
}

type Stats struct {
	Steps            int
	ModelTime        time.Duration
	ToolTime         time.Duration
	PromptTokens     int
	LastPromptTokens int
	CompletionTokens int
	RouterTime       time.Duration
}

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
	return fmt.Sprintf("· %s · %d steps · model %s · tools %s · %s in / %s out",
		intent, s.Steps, formatDuration(s.ModelTime), formatDuration(s.ToolTime),
		FormatTokens(s.PromptTokens), FormatTokens(s.CompletionTokens))
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

func FormatTrace(step int, think bool, d time.Duration, in, out, window int, names []string) string {
	th := "off"
	if think {
		th = "on"
	}
	pct := 0
	if window > 0 {
		pct = int(float64(in)*100/float64(window) + 0.5)
	}
	return fmt.Sprintf("  [step %d] think=%s  %s  in=%s out=%s  ctx %d%% (%s/%s)  -> %s",
		step, th, formatDuration(d), FormatTokens(in), FormatTokens(out),
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
	fmt.Fprintln(w, ui.Current().Muted(FormatSummary(intent, s)))
}

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

func PrintTrace(w io.Writer, step int, think bool, d time.Duration, in, out, window int, names []string) {
	if w == nil {
		return
	}
	fmt.Fprintln(w, FormatTrace(step, think, d, in, out, window, names))
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
	callLine, rejectLine := ui.RejectCall(name, shortArgSummary(args), reason)
	fmt.Fprintln(w, callLine)
	fmt.Fprintln(w, rejectLine)
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

func longerText(a, b string) string {
	a = trimLeadingBlanks(a)
	b = trimLeadingBlanks(b)
	if utf8.RuneCountInString(a) > utf8.RuneCountInString(b) {
		return a
	}
	return b
}

var (
	ErrAborted         = errors.New("agent: aborted")
	ErrMaxSteps        = errors.New("agent: max steps exceeded")
	ErrNoAnswer        = errors.New("agent: no answer")
	ErrContextOverflow = errors.New("konsolai: the conversation no longer fits the model's context; start a new session with `new`.")
)

const (
	elidedPrefix      = "[tool output elided:"
	elidedReadPrefix  = "[elided] "
	contextSafety     = 1024
	contextTrigger    = 0.60
	contextTarget     = 0.40
	minOutputTokens   = 2048
	protectLastMsgs   = 6
	elideSummaryMax   = 80
	elideOutlineCap   = 1536
	postPlanNoteAt    = 4
	postPlanForceAt   = 6
	postPlanDocsAt    = 3
	docsExportNoteAt  = 2
	docsExportForceAt = 3
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

func shouldElide(estimate, window, cap int) bool {
	if window <= 0 {
		return false
	}
	if float64(estimate) > contextTrigger*float64(window) {
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

func PrepareCall(client *llm.Client, msgs []llm.Message, tools []llm.ToolSpec, intent string) (estimate, maxTokens int, elide ElideStats, err error) {
	return prepareCall(client, msgs, tools, intent, nil, nil)
}

func prepareCall(client *llm.Client, msgs []llm.Message, tools []llm.ToolSpec, intent string, spans *[]readSpan, seen map[string]struct{}) (estimate, maxTokens int, elide ElideStats, err error) {
	window := client.ContextWindow()
	cap := ProfileCap(intent)
	estimate = client.EstimatePromptTokens(msgs, tools)
	if shouldElide(estimate, window, cap) {
		elide.Before = estimate
		for {
			est := client.EstimatePromptTokens(msgs, tools)
			if float64(est) <= contextTarget*float64(window) {
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

func Run(ctx context.Context, cfg Config, systemContext, task string) error {
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = ActPrompt
	}
	temp := cfg.Temperature
	if temp == 0 {
		temp = 0.2
	}
	rec := cfg.Recorder
	if rec == nil {
		rec = NopRecorder{}
	}
	if _, err := cfg.Client.ResolveModel(ctx); err != nil {
		return err
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: prompt + "\n\n" + systemContext},
	}
	msgs = append(msgs, cfg.History...)
	userMsg := llm.Message{Role: llm.RoleUser, Content: task}
	msgs = append(msgs, userMsg)
	userIdx := len(msgs) - 1
	_ = rec.Message(ctx, userMsg)
	specs := cfg.Tools.Specs()
	seenFail := make(map[string]struct{})
	seenRead := make(map[string]struct{})
	var readSpans []readSpan
	planApproved := cfg.ResumePlan
	planFiles := cfg.PlanFiles
	planCmds := cfg.PlanCmds
	planText := cfg.PlanText
	if planApproved && cfg.Guard != nil {
		cfg.Guard.AllowPlanFiles(planFiles)
	}
	explore := 0
	noteSent := false
	readonlyAfterNote := 0
	sourceReads := 0
	toolsGone := false
	goneNoteAdded := false
	wroteFile := false
	exportedDoc := false
	docsNudged := false
	docsWriteNoted := false
	docsExportNoted := false
	docsExportWait := 0
	postPlanReads := 0
	forceMutating := false
	forceExport := false
	shellFail := false
	planRejected := false
	approvedAll := false
	consecFail := 0
	var st Stats
	st.RouterTime = cfg.RouterTime
	defer func() {
		if cfg.Stats != nil {
			*cfg.Stats = st
		}
		if cfg.Intent != "" || cfg.Err != nil {
			intent := cfg.Intent
			if intent == "" {
				intent = "act"
			}
			PrintSummary(cfg.Err, intent, st)
		}
	}()

	for step := 0; step < analysisLimit(cfg, sourceReads); step++ {
		think := cfg.ThinkingPolicy.enabled(planApproved, shellFail, consecFail)
		if cfg.ThinkingPolicy == ThinkPlanStep && planRejected && !planApproved {
			think = true
		}
		opts := llm.ChatOptions{Thinking: think, Temperature: temp}
		shellFail = false
		forcingPlan := toolsGone && cfg.RequirePlan && !planApproved
		if forcingPlan && cfg.ThinkingPolicy == ThinkPlanStep {
			think = true
			opts.Thinking = true
		}
		label := "working"
		if cfg.RequirePlan && !planApproved {
			label = "planning"
		} else if think {
			label = "thinking"
		}
		if toolsGone && !goneNoteAdded {
			note := ToolsGoneNote
			if forcingPlan {
				note = ForcedPlanNote
			}
			gone := llm.Message{Role: llm.RoleUser, Content: note}
			msgs = append(msgs, gone)
			_ = rec.Message(ctx, gone)
			goneNoteAdded = true
		}
		stepSpecs := specs
		if toolsGone {
			if forcingPlan {
				stepSpecs = planOnlySpecs(specs)
				opts.ToolChoice = "propose_plan"
			} else if !cfg.RequirePlan {
				stepSpecs = nil
			}
		} else if forceExport && planApproved && !exportedDoc {
			stepSpecs = exportOnlySpecs(specs)
			opts.ToolChoice = "export_doc"
		} else if forceMutating && planApproved {
			stepSpecs = mutatingSpecs(specs)
			opts.ToolChoice = "required"
		}
		_, maxTok, elide, err := prepareCall(cfg.Client, msgs, stepSpecs, cfg.Intent, &readSpans, seenRead)
		if err != nil {
			return err
		}
		if cfg.Trace && elide.Count > 0 {
			fmt.Fprintln(cfg.Err, FormatElide(elide, cfg.Client.ContextWindow()))
		}
		opts.MaxTokens = maxTok
		live := ui.StartLive(label)
		opts.OnProgress = func(p llm.Progress) {
			live.SetLabel(ProgressLabel(label, p))
		}
		t0 := time.Now()
		msg, usage, err := cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
		modelElapsed := time.Since(t0)
		live.Stop()
		if errors.Is(err, llm.ErrTruncated) {
			msgs = aggressiveTrim(msgs, userIdx)
			userIdx = lastUserTask(msgs, task)
			est := cfg.Client.EstimatePromptTokens(msgs, stepSpecs)
			window := cfg.Client.ContextWindow()
			room := window - est - contextSafety
			if room < minOutputTokens {
				return ErrContextOverflow
			}
			opts.MaxTokens = room
			live = ui.StartLive(label)
			opts.OnProgress = func(p llm.Progress) {
				live.SetLabel(ProgressLabel(label, p))
			}
			t0 = time.Now()
			msg, usage, err = cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
			modelElapsed += time.Since(t0)
			live.Stop()
			if errors.Is(err, llm.ErrTruncated) {
				return ErrContextOverflow
			}
		}
		if err != nil {
			return err
		}
		if cfg.ThinkingPolicy == ThinkPlanStep && !think && !planApproved && hasProposePlan(msg) && proposePlanValid(cfg.Tools, msg) {
			if cfg.Trace {
				st.Steps++
				st.ModelTime += modelElapsed
				st.PromptTokens += usage.PromptTokens
				st.CompletionTokens += usage.CompletionTokens
				if usage.PromptTokens > 0 {
					st.LastPromptTokens = usage.PromptTokens
				}
				dumpTrace(cfg.SessionID, st.Steps, msg)
				PrintTrace(cfg.Err, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, cfg.Client.ContextWindow(), FormatTraceCalls(msg.ToolCalls))
			} else {
				st.Steps++
				st.ModelTime += modelElapsed
				st.PromptTokens += usage.PromptTokens
				st.CompletionTokens += usage.CompletionTokens
				if usage.PromptTokens > 0 {
					st.LastPromptTokens = usage.PromptTokens
				}
			}
			think = true
			opts.Thinking = true
			opts.ToolChoice = "propose_plan"
			stepSpecs = planOnlySpecs(specs)
			if len(stepSpecs) == 0 {
				stepSpecs = specs
			}
			label = "thinking"
			_, maxTok, elide, err = prepareCall(cfg.Client, msgs, stepSpecs, cfg.Intent, &readSpans, seenRead)
			if err != nil {
				return err
			}
			if cfg.Trace && elide.Count > 0 {
				fmt.Fprintln(cfg.Err, FormatElide(elide, cfg.Client.ContextWindow()))
			}
			opts.MaxTokens = maxTok
			live = ui.StartLive(label)
			opts.OnProgress = func(p llm.Progress) {
				live.SetLabel(ProgressLabel(label, p))
			}
			t0 := time.Now()
			msg, usage, err = cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
			modelElapsed = time.Since(t0)
			live.Stop()
			if errors.Is(err, llm.ErrTruncated) {
				msgs = aggressiveTrim(msgs, userIdx)
				userIdx = lastUserTask(msgs, task)
				est := cfg.Client.EstimatePromptTokens(msgs, stepSpecs)
				window := cfg.Client.ContextWindow()
				room := window - est - contextSafety
				if room < minOutputTokens {
					return ErrContextOverflow
				}
				opts.MaxTokens = room
				live = ui.StartLive(label)
				opts.OnProgress = func(p llm.Progress) {
					live.SetLabel(ProgressLabel(label, p))
				}
				t0 = time.Now()
				msg, usage, err = cfg.Client.Complete(ctx, msgs, stepSpecs, opts)
				modelElapsed += time.Since(t0)
				live.Stop()
				if errors.Is(err, llm.ErrTruncated) {
					return ErrContextOverflow
				}
			}
			if err != nil {
				return err
			}
		}
		if forcingPlan {
			msg.Content = ""
		}
		st.Steps++
		st.ModelTime += modelElapsed
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		if usage.PromptTokens > 0 {
			st.LastPromptTokens = usage.PromptTokens
		}
		if cfg.Trace {
			dumpTrace(cfg.SessionID, st.Steps, msg)
			PrintTrace(cfg.Err, st.Steps, think, modelElapsed, usage.PromptTokens, usage.CompletionTokens, cfg.Client.ContextWindow(), FormatTraceCalls(msg.ToolCalls))
		}
		narration := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(narration) != "" && len(msg.ToolCalls) > 0 && !hasFinishCall(msg) {
			fmt.Fprintln(cfg.Out, ui.Current().Muted(narration))
		}
		if len(msg.ToolCalls) > 0 {
			msg.Content = truncateNarration(msg.Content)
		}
		msgs = append(msgs, msg)
		_ = rec.Message(ctx, msg)

		if len(msg.ToolCalls) == 0 {
			if cfg.Task == "docs" && planApproved && !wroteFile && !exportedDoc && !docsNudged {
				docsNudged = true
				note := DocsFileNote
				if wantsDocExport(task, planFiles) {
					note = DocsExportNote
				}
				nudge := llm.Message{Role: llm.RoleUser, Content: note}
				msgs = append(msgs, nudge)
				_ = rec.Message(ctx, nudge)
				continue
			}
			text := trimLeadingBlanks(msg.Content)
			if strings.TrimSpace(text) == "" {
				return ErrNoAnswer
			}
			fmt.Fprintln(cfg.Out)
			writeAnswer(cfg.Out, text, cfg.Plain)
			return nil
		}

		noteWasSent := noteSent
		stepReadOnly := 0
		stepOther := 0
		lastReadOnly := -1
		for _, call := range msg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			tool, ok := cfg.Tools.Get(call.Function.Name)
			args := json.RawMessage(call.Function.Arguments)
			if !ok {
				reason := "unknown tool " + call.Function.Name
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + reason,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				continue
			}
			action, err := tool.Prepare(call.Function.Arguments)
			if err != nil {
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, err.Error())
				content := "error: " + err.Error()
				failKey := call.Function.Name + call.Function.Arguments + "prep"
				if _, dup := seenFail[failKey]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					seenFail[failKey] = struct{}{}
				}
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    content,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Decision: "invalid", IsError: true, Output: tm.Content})
				continue
			}
			kind := action.Kind()
			if kind == tools.Mutating && cfg.RequirePlan && !planApproved {
				reason := "call propose_plan first"
				printReject(cfg.Out, call.Function.Name, call.Function.Arguments, reason)
				tm := llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + reason,
					ToolCallID: call.ID,
				}
				msgs = append(msgs, tm)
				_ = rec.Message(ctx, tm)
				_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "gate_blocked", IsError: true, Output: tm.Content})
				continue
			}
			if kind == tools.ReadOnly {
				stepReadOnly++
			} else if kind != tools.Plan {
				stepOther++
			}
			decision := "auto"
			summary := action.Summary()
			outside := false
			inPlanHit := false
			if kind == tools.Mutating && cfg.RequirePlan && planApproved {
				if p, ok := filePathOf(action); ok {
					if inPlan(planFiles, p) {
						inPlanHit = true
					} else {
						outside = true
					}
				} else if c, ok := commandOf(action); ok {
					okHit, hits := safety.MatchPlan(safety.Plan{Files: planFiles, Commands: planCmds, Text: planText}, c)
					if cfg.Trace {
						for _, h := range hits {
							mark := "✗"
							if h.OK {
								mark = "✓"
							}
							fmt.Fprintln(cfg.Err, h.Rule+" "+mark)
						}
					}
					if okHit {
						inPlanHit = true
					} else {
						outside = true
					}
				} else {
					outside = true
				}
				if outside {
					summary = "⚠ outside plan: " + summary
				}
			}
			if inPlanHit {
				summary += " " + ui.Current().Muted("(in plan)")
			}
			if !inPlanHit && kind != tools.ReadOnly && kind != tools.Final {
				_, isCmd := commandOf(action)
				if outside && approvedAll && isCmd && !cfg.AutoApprove {
					summary += " " + ui.Current().Muted("(outside plan, approved by a)")
					decision = "approved_all"
				} else {
					var d ui.Decision
					var reason string
					var err error
					if outside && !cfg.AutoApprove {
						d, reason, err = cfg.Confirmer.ConfirmForced(summary, action.Preview())
					} else {
						if outside && cfg.AutoApprove {
							fmt.Fprintln(cfg.Out, ui.Current().Warn(summary))
						}
						d, reason, err = cfg.Confirmer.Confirm(summary, action.Preview())
					}
					if err != nil {
						return err
					}
					switch d {
					case ui.Reject:
						if kind == tools.Plan {
							planRejected = true
						}
						tm := llm.Message{
							Role:       llm.RoleTool,
							Content:    "user rejected: " + reason,
							ToolCallID: call.ID,
						}
						msgs = append(msgs, tm)
						_ = rec.Message(ctx, tm)
						_ = rec.Action(ctx, ActionRecord{Tool: call.Function.Name, Args: args, Kind: kind, Decision: "rejected", RejectReason: reason, IsError: true, Output: tm.Content})
						continue
					case ui.Quit:
						return ErrAborted
					case ui.Approve:
						decision = "approved"
						if cfg.AutoApprove {
							decision = "auto"
						}
						if kind == tools.Plan {
							planApproved = true
							planRejected = false
							planFiles = planFilesOf(action)
							planCmds = planCmdsOf(action)
							planText = planTextOf(action)
							if cfg.Guard != nil {
								cfg.Guard.AllowPlanFiles(planFiles)
							}
						}
					case ui.ApproveAll:
						decision = "approved_all"
						approvedAll = true
						if cfg.AutoApprove {
							decision = "auto"
						}
						if kind == tools.Plan {
							planApproved = true
							planRejected = false
							planFiles = planFilesOf(action)
							planCmds = planCmdsOf(action)
							planText = planTextOf(action)
							if cfg.Guard != nil {
								cfg.Guard.AllowPlanFiles(planFiles)
							}
						}
					}
				}
			}
			finish := action.Kind() == tools.Final
			if call.Function.Name == "read_file" {
				if skipped, tm := skipCoveredRead(action, call, readSpans, msgs, cfg.Out); skipped {
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					lastReadOnly = len(msgs) - 1
					continue
				}
			}
			if kind == tools.ReadOnly && call.Function.Name != "read_file" {
				key := call.Function.Name + "\x00" + canonicalJSON(call.Function.Arguments)
				if _, dup := seenRead[key]; dup {
					if !finish {
						fmt.Fprintln(cfg.Out, ui.Current().Accent("▸")+" "+summary+" (duplicate, skipped)")
					}
					tm := llm.Message{
						Role: llm.RoleTool, Content: "note: you already made this exact call; its result is above.",
						ToolCallID: call.ID,
					}
					msgs = append(msgs, tm)
					_ = rec.Message(ctx, tm)
					lastReadOnly = len(msgs) - 1
					continue
				}
				seenRead[key] = struct{}{}
			}
			if !finish {
				fmt.Fprintln(cfg.Out, ui.Current().Accent("▸")+" "+summary)
			}
			start := time.Now()
			result, err := action.Run(ctx)
			elapsed := time.Since(start)
			st.ToolTime += elapsed
			if err != nil {
				return err
			}
			if result.Final {
				result.Output = longerText(narration, result.Output)
			}
			if call.Function.Name == "run_shell" && cfg.OnShell != nil {
				cfg.OnShell(result.Output)
			}
			_ = rec.Action(ctx, ActionRecord{
				Tool: call.Function.Name, Args: args, Kind: kind, Decision: decision,
				IsError: result.IsError, Meta: result.Meta, Duration: elapsed, Output: result.Output,
			})
			if result.Change != nil {
				_ = rec.FileChange(ctx, *result.Change)
			}
			if !result.IsError {
				switch call.Function.Name {
				case "write_file":
					wroteFile = true
				case "export_doc":
					exportedDoc = true
				case "read_file":
					if p, ok := action.(interface{ ResolvedPath() string }); ok {
						if hash, err := fileSHA256(p.ResolvedPath()); err == nil {
							start, end, okHdr := parseReadHeader(result.Output)
							if !okHdr {
								start, end = parseReadRange(call.Function.Arguments)
								if end < 1 {
									end = fileLineCount(p.ResolvedPath())
								}
							}
							if start > 0 && end >= start {
								readSpans = append(readSpans, readSpan{
									abs: p.ResolvedPath(), hash: hash,
									start: start, end: end, step: st.Steps, toolID: call.ID,
								})
							}
						}
					}
				}
			}
			if result.Final {
				text := trimLeadingBlanks(result.Output)
				if strings.TrimSpace(text) == "" {
					return ErrNoAnswer
				}
				fmt.Fprintln(cfg.Out)
				writeAnswer(cfg.Out, text, cfg.Plain)
				return nil
			}
			pal := ui.Current()
			mark := pal.Success("✓")
			if result.IsError {
				mark = pal.Error("✗")
			}
			fmt.Fprintf(cfg.Out, "%s %s · %s\n", mark, pal.Muted(displayMeta(result)), pal.Muted(formatDuration(elapsed)))
			content := result.Output
			if cfg.Task == "analysis" && analysisSourceRead(call.Function.Name, call.Function.Arguments) && !result.IsError {
				sourceReads++
			}
			if result.IsError {
				consecFail++
				sum := sha256.Sum256([]byte(result.Output))
				key := call.Function.Name + call.Function.Arguments + string(sum[:])
				if _, dup := seenFail[key]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					seenFail[key] = struct{}{}
				}
			} else {
				consecFail = 0
			}
			tm := llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: call.ID,
			}
			msgs = append(msgs, tm)
			_ = rec.Message(ctx, tm)
			if kind == tools.ReadOnly {
				lastReadOnly = len(msgs) - 1
			}
			if call.Function.Name == "run_shell" && result.IsError {
				shellFail = true
			}
		}
		if planApproved && (cfg.RequirePlan || cfg.Intent == "act" || cfg.Intent == "build") {
			if stepReadOnly > 0 && stepOther == 0 {
				postPlanReads++
				if cfg.Task == "docs" && !wroteFile && postPlanReads == postPlanDocsAt && !docsWriteNoted {
					docsWriteNoted = true
					nudge := llm.Message{Role: llm.RoleUser, Content: docsPlannedWriteNote(planFiles)}
					msgs = append(msgs, nudge)
					_ = rec.Message(ctx, nudge)
				}
				if postPlanReads == postPlanNoteAt {
					nudge := llm.Message{Role: llm.RoleUser, Content: PostPlanNote}
					msgs = append(msgs, nudge)
					_ = rec.Message(ctx, nudge)
				}
				if postPlanReads >= postPlanForceAt {
					forceMutating = true
				}
			} else if stepOther > 0 {
				postPlanReads = 0
				forceMutating = false
			}
		}
		if cfg.Task == "docs" && wroteFile && !exportedDoc {
			docsExportWait++
			if docsExportWait == docsExportNoteAt && !docsExportNoted {
				docsExportNoted = true
				nudge := llm.Message{Role: llm.RoleUser, Content: DocsExportNote}
				msgs = append(msgs, nudge)
				_ = rec.Message(ctx, nudge)
			}
			if docsExportWait >= docsExportForceAt {
				forceExport = true
			}
		} else if exportedDoc {
			docsExportWait = 0
			forceExport = false
		}
		prePlanEvery := !planApproved && (cfg.RequirePlan || cfg.Intent == "act" || cfg.Intent == "build")
		if prePlanEvery && (stepReadOnly > 0 || stepOther > 0) {
			explore++
			if cfg.ExploreBudget > 0 && explore >= cfg.ExploreBudget && cfg.ExploreNote != "" && lastReadOnly >= 0 {
				msgs[lastReadOnly].Content += "\n\n" + exploreNote(cfg, sourceReads)
				noteSent = true
			}
			if noteWasSent {
				readonlyAfterNote++
				if readonlyAfterNote >= hardStopAfter(cfg, sourceReads) {
					toolsGone = true
				}
			}
		} else if stepOther > 0 {
			explore = 0
		} else if stepReadOnly > 0 && (!cfg.RequirePlan || !planApproved) {
			explore++
			if cfg.ExploreBudget > 0 && explore >= cfg.ExploreBudget && cfg.ExploreNote != "" && lastReadOnly >= 0 {
				msgs[lastReadOnly].Content += "\n\n" + exploreNote(cfg, sourceReads)
				noteSent = true
			}
			if noteWasSent {
				readonlyAfterNote++
				if readonlyAfterNote >= hardStopAfter(cfg, sourceReads) {
					toolsGone = true
				}
			}
		}
	}
	note := llm.Message{Role: llm.RoleUser, Content: MaxStepsNote}
	msgs = append(msgs, note)
	_ = rec.Message(ctx, note)
	_, maxTok, elide, prepErr := PrepareCall(cfg.Client, msgs, nil, cfg.Intent)
	if prepErr != nil {
		return prepErr
	}
	if cfg.Trace && elide.Count > 0 {
		fmt.Fprintln(cfg.Err, FormatElide(elide, cfg.Client.ContextWindow()))
	}
	live := ui.StartLive("thinking")
	t0 := time.Now()
	msg, usage, err := cfg.Client.Complete(ctx, msgs, nil, llm.ChatOptions{
		Thinking: false, Temperature: temp, MaxTokens: maxTok,
		OnProgress: func(p llm.Progress) { live.SetLabel(ProgressLabel("thinking", p)) },
	})
	live.Stop()
	if err != nil {
		if cfg.Trace {
			fmt.Fprintln(cfg.Err, "max-steps summary:", err)
		}
	} else {
		st.Steps++
		st.ModelTime += time.Since(t0)
		st.PromptTokens += usage.PromptTokens
		st.CompletionTokens += usage.CompletionTokens
		if usage.PromptTokens > 0 {
			st.LastPromptTokens = usage.PromptTokens
		}
		text := trimLeadingBlanks(msg.Content)
		if strings.TrimSpace(text) != "" {
			fmt.Fprintln(cfg.Out)
			writeAnswer(cfg.Out, text, cfg.Plain)
		}
	}
	return ErrMaxSteps
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

func writeAnswer(w io.Writer, text string, plain bool) {
	if plain {
		fmt.Fprintln(w, text)
		return
	}
	r := ui.NewRenderer(w, false)
	_, _ = r.Write([]byte(text))
	if !strings.HasSuffix(text, "\n") {
		_, _ = r.Write([]byte("\n"))
	}
	_ = r.Flush()
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
		fmt.Fprintln(out, ui.Current().Accent("▸")+" "+summary+" (already shown, skipped)")
	}
	return true, llm.Message{
		Role:       llm.RoleTool,
		Content:    fmt.Sprintf("already in context above (step %d, lines %d-%d)", sp.step, sp.start, sp.end),
		ToolCallID: call.ID,
	}
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
