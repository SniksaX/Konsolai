package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/siraj/konsolai/internal/llm"
)

type Intent string

const (
	Answer Intent = "answer"
	Lookup Intent = "lookup"
	Act    Intent = "act"
	Build  Intent = "build"
)

type Task string

const (
	TaskAnalysis Task = "analysis"
	TaskDocs     Task = "docs"
	TaskOther    Task = "other"
)

type Candidate struct {
	Title    string
	LastUser string
	Age      time.Duration
}

type Effort string

const (
	EffortNone Effort = "none"
	EffortLow  Effort = "low"
	EffortHigh Effort = "high"
)

type Decision struct {
	Intent           Intent `json:"intent"`
	Language         string `json:"language"`
	Reason           string `json:"reason"`
	Reasoning        bool   `json:"reasoning"`
	Effort           Effort `json:"effort"`
	Task             Task   `json:"task"`
	ContinuesSession int    `json:"continues_session"`
}

func (d Decision) EffectiveEffort() Effort {
	switch d.Effort {
	case EffortNone, EffortLow, EffortHigh:
		return d.Effort
	}
	if d.Reasoning {
		return EffortHigh
	}
	return EffortLow
}

const routerPrompt = `Classify the user's request into exactly one intent.
answer: greetings, questions, explanations, fixing or explaining a command, error or snippet the user pasted; anything answerable from general knowledge.
lookup: needs facts from this machine or project (files, versions, processes, ports) but changes nothing.
act: perform operations on the system (create, move or delete files and dirs, install, run commands, git).
build: write, modify or document code in a project (features, fixes, refactors, tests, docs, diagrams, exports).
If the user explicitly asks to search online, look something up on the web, or check a URL -> lookup.
References to the current location or its contents ('this', 'this one', 'here', 'this folder', 'the code here', 'ce dossier', 'ici', 'ce projet') mean the current directory -> lookup (or build if changes are requested). Never answer these without looking.
When unsure between answer and anything else, choose answer if the message is a question.
language is the ISO 639-1 code of the user's message.
reasoning=true when the request needs multi-step analysis: debugging an error or a pasted command, comparing approaches, non-trivial code questions. false for greetings, simple facts and simple commands.
effort is required: "none" | "low" | "high".
none: greetings, thanks, acknowledgements ('ok', 'good', 'merci', 'okey good'), small talk.
low: simple questions or single commands with an obvious answer. A single explicit command or a trivial operation is low.
high: debugging, design or trade-off questions, analysis, multi-step tasks, anything with many constraints.
Acknowledgements and short reactions are ALWAYS intent answer, effort none, even after an act or build turn.
task is "analysis" when the user asks to analyse, review or give an opinion on code; "docs" when they ask to generate documentation, READMEs, diagrams or reports about code; otherwise "other".
continues_session: Pick the conversation this message follows up, or 0 if it starts a new topic. Prefer 0 when unsure.
Prefer 0 (new topic) when the new request is a different task, even about the same project (e.g. a PDF overview after a README request).
Never print a secret value in full. Refer to it by name and its first 4 characters.`

var decisionSchema = json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","enum":["answer","lookup","act","build"]},"language":{"type":"string"},"reason":{"type":"string"},"reasoning":{"type":"boolean"},"effort":{"type":"string","enum":["none","low","high"]},"task":{"type":"string","enum":["analysis","docs","other"]},"continues_session":{"type":"integer"}},"required":["intent","language","reason","reasoning","effort","task","continues_session"],"additionalProperties":false}`)

func Classify(ctx context.Context, client *llm.Client, request string, candidates []Candidate, inRepo bool) (Decision, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sys := routerPrompt
	if len(candidates) == 0 {
		sys += "\nThere is no previous conversation."
	} else {
		sys += "\n" + FormatCandidates(candidates)
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: sys},
		{Role: llm.RoleUser, Content: request},
	}
	msg, _, err := client.Complete(ctx, msgs, nil, llm.ChatOptions{
		Thinking: false, Temperature: 0, MaxTokens: 160, ResponseSchema: decisionSchema,
	})
	if err != nil {
		return fallback(request, inRepo), err
	}
	d, ok := parseDecision(msg.Content)
	if !ok {
		return fallback(request, inRepo), nil
	}
	if d.ContinuesSession < 0 || d.ContinuesSession > len(candidates) {
		d.ContinuesSession = 0
	}
	return d, nil
}

func FormatCandidates(cands []Candidate) string {
	var b strings.Builder
	b.WriteString("Recent conversations (most recent first):")
	for i, c := range cands {
		last := c.LastUser
		if last == "" {
			last = c.Title
		}
		fmt.Fprintf(&b, "\n%d. [%s] %s — last: %s", i+1, formatAge(c.Age), c.Title, last)
	}
	return b.String()
}

func formatAge(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

func extractFirstJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(raw); i++ {
		c := raw[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}

func parseDecision(raw string) (Decision, bool) {
	raw = extractFirstJSON(raw)
	if raw == "" {
		return Decision{}, false
	}
	var d Decision
	if json.Unmarshal([]byte(raw), &d) != nil {
		return Decision{}, false
	}
	switch d.Intent {
	case Answer, Lookup, Act, Build:
	default:
		return Decision{}, false
	}
	if d.Language == "" {
		d.Language = "en"
	}
	switch d.Effort {
	case EffortNone, EffortLow, EffortHigh:
	case "":
		if d.Reasoning {
			d.Effort = EffortHigh
		} else {
			d.Effort = EffortLow
		}
	default:
		return Decision{}, false
	}
	switch d.Task {
	case TaskAnalysis, TaskDocs, TaskOther:
	case "":
		d.Task = TaskOther
	default:
		return Decision{}, false
	}
	if d.ContinuesSession < 0 {
		d.ContinuesSession = 0
	}
	return d, true
}

func fallback(request string, inRepo bool) Decision {
	q := strings.HasSuffix(strings.TrimSpace(request), "?")
	d := Decision{Language: "en", Reason: "fallback", Task: TaskOther, ContinuesSession: 0, Effort: EffortLow}
	switch {
	case q && inRepo:
		d.Intent = Lookup
	case q:
		d.Intent = Answer
	default:
		d.Intent = Act
	}
	return d
}

func Schema() json.RawMessage { return decisionSchema }
