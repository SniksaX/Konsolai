package intent

import (
	"context"
	"encoding/json"
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
	TaskOther    Task = "other"
)

type Decision struct {
	Intent    Intent `json:"intent"`
	Language  string `json:"language"`
	Reason    string `json:"reason"`
	Reasoning bool   `json:"reasoning"`
	Task      Task   `json:"task"`
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
task is "analysis" when the user asks to analyse, review or give an opinion on code; otherwise "other".`

var decisionSchema = json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","enum":["answer","lookup","act","build"]},"language":{"type":"string"},"reason":{"type":"string"},"reasoning":{"type":"boolean"},"task":{"type":"string","enum":["analysis","other"]}},"required":["intent","language","reason","reasoning","task"],"additionalProperties":false}`)

func Classify(ctx context.Context, client *llm.Client, request string, recent []llm.Message, inRepo bool) (Decision, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	msgs := []llm.Message{{Role: llm.RoleSystem, Content: routerPrompt}}
	for _, m := range lastNonTool(recent, 4) {
		msgs = append(msgs, m)
	}
	msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: request})
	msg, _, err := client.Complete(ctx, msgs, nil, llm.ChatOptions{
		Thinking: false, Temperature: 0, MaxTokens: 120, ResponseSchema: decisionSchema,
	})
	if err != nil {
		return fallback(request, inRepo), err
	}
	d, ok := parseDecision(msg.Content)
	if !ok {
		return fallback(request, inRepo), nil
	}
	return d, nil
}

func lastNonTool(msgs []llm.Message, n int) []llm.Message {
	var out []llm.Message
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		if msgs[i].Role == llm.RoleTool {
			continue
		}
		out = append(out, msgs[i])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func parseDecision(raw string) (Decision, bool) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			raw = raw[i : j+1]
		}
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
	switch d.Task {
	case TaskAnalysis, TaskOther:
	case "":
		d.Task = TaskOther
	default:
		return Decision{}, false
	}
	return d, true
}

func fallback(request string, inRepo bool) Decision {
	q := strings.HasSuffix(strings.TrimSpace(request), "?")
	switch {
	case q && inRepo:
		return Decision{Intent: Lookup, Language: "en", Reason: "fallback"}
	case q:
		return Decision{Intent: Answer, Language: "en", Reason: "fallback"}
	default:
		return Decision{Intent: Act, Language: "en", Reason: "fallback"}
	}
}

func Schema() json.RawMessage { return decisionSchema }
