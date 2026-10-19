package turn

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/embed"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/rag"
	"github.com/SniksaX/Konsolai/internal/ui"
)

type ThinkMode int

const (
	ThinkAuto ThinkMode = iota
	ThinkAlways
	ThinkNever
)

func ParseThinkMode(s string) (ThinkMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on":
		return ThinkAlways, true
	case "off":
		return ThinkNever, true
	case "auto", "":
		return ThinkAuto, true
	default:
		return ThinkAuto, false
	}
}

type Runner struct {
	Profile          config.Profile
	Client           *llm.Client
	FastClient       *llm.Client
	ThinkMode        ThinkMode
	RouterSource     string
	RouterModel      string
	Mem              *memory.Guard
	Out              io.Writer
	Err              io.Writer
	OpenConfirm      func(yes bool) (agent.Confirmer, func(), error)
	Copy             func(ctx context.Context, text string) (backend string, err error)
	Version          string
	LastPromptTokens int
	LastWindow       int
	SlashHelp        string
	Ingest           *rag.Ingester
	Embed            *embed.Client
	shellOut         []string
	lastCode         int
	lastIntent       intent.Intent
	lastLang         string
	lastEffort       intent.Effort
	lastTask         intent.Task
	lastUser         string
	lastAssistant    string
	lastPromise      bool
	lastEscalate     intent.Intent
	escalated        bool
	RebindNext       bool
	statMu           sync.Mutex
	statOn           bool
	statIntent       string
	statSession      string
}

type Request struct {
	Text          string
	Stdin         string
	Intent        string
	Effort        string
	Yes           bool
	Trace         bool
	Plain         bool
	New           bool
	ResumeSession string
	KeepOpen      bool
	Cwd           string
}

type Result struct {
	SessionID    string
	Intent       string
	ExitCode     int
	Answer       string
	PromptTokens int
	Window       int
}

type ResumeCandidate struct {
	ID       string
	Title    string
	LastUser string
	Age      time.Duration
	Messages []llm.Message
}

func (r *Runner) out() io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return os.Stdout
}

func (r *Runner) errw() io.Writer {
	if r.Err != nil {
		return r.Err
	}
	return os.Stderr
}

func (r *Runner) client() *llm.Client {
	if r.Client == nil {
		c := llm.New(r.Profile.MainEndpoint(), r.Profile.MainModelID())
		c.SetIdleTimeout(time.Duration(r.Profile.LLM.IdleTimeoutSeconds) * time.Second)
		c.SetThinkKwargs(r.Profile.MainThinking())
		if n := r.Profile.Main().Context; n > 0 {
			c.PinContext(n)
		}
		c.SetRetry(r.Profile.LLM.Retries, time.Duration(r.Profile.LLM.RetryWaitMS)*time.Millisecond)
		r.Client = c
	}
	r.attachStats(r.Client)
	return r.Client
}

func (r *Runner) fastClient() *llm.Client {
	if r.FastClient != nil {
		r.attachStats(r.FastClient)
		return r.FastClient
	}
	if !r.Profile.FastOn() {
		return nil
	}
	ep := r.Profile.FastEndpoint()
	if ep == "" {
		return nil
	}
	c := llm.NewFast(ep, r.Profile.FastModelID())
	c.SetIdleTimeout(time.Duration(r.Profile.LLM.IdleTimeoutSeconds) * time.Second)
	c.SetThinkKwargs(r.Profile.FastThinking())
	if fm, ok := r.Profile.FastModel(); ok && fm.Context > 0 {
		c.PinContext(fm.Context)
	}
	c.SetRetry(r.Profile.LLM.Retries, time.Duration(r.Profile.LLM.RetryWaitMS)*time.Millisecond)
	r.FastClient = c
	r.attachStats(c)
	return c
}

func (r *Runner) routerClient(ctx context.Context) *llm.Client {
	if fc := r.fastClient(); fc != nil && fc.Reachable(ctx) {
		r.RouterSource = "fast"
		return fc
	}
	r.RouterSource = "main"
	return r.client()
}

func (r *Runner) mem() *memory.Guard {
	if r.Mem != nil {
		return r.Mem
	}
	return memory.NewGuard(nil, nil)
}

func (r *Runner) confirm(ctx context.Context, yes bool) (agent.Confirmer, func(), error) {
	var conf agent.Confirmer
	var closeConf func()
	if r.OpenConfirm != nil {
		c, closer, err := r.OpenConfirm(yes)
		if err != nil {
			return nil, func() {}, err
		}
		conf, closeConf = c, closer
	} else {
		p, err := ui.OpenPrompter(yes)
		if err != nil {
			return nil, func() {}, err
		}
		conf = p
		closeConf = func() { _ = p.Close() }
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if p, ok := conf.(*ui.Prompter); ok {
				p.Cancel()
			}
		case <-stop:
		}
	}()
	return conf, func() { close(stop); closeConf() }, nil
}

var thinkPrefixes = []string{"think hard", "réfléchis bien"}

func StripThinkPrefix(s string) (string, bool) {
	t := strings.TrimSpace(s)
	lower := strings.ToLower(t)
	for _, p := range thinkPrefixes {
		if strings.HasPrefix(lower, p) {
			rest := strings.TrimSpace(t[len(p):])
			rest = strings.TrimLeft(rest, ",:;- ")
			if rest == "" {
				return s, true
			}
			return rest, true
		}
	}
	return s, false
}

func answerThink(dec *intent.Decision, policy string, mode ThinkMode) bool {
	if mode == ThinkNever {
		return false
	}
	if mode == ThinkAlways {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "on":
		return true
	case "off":
		return false
	default:
		return dec != nil && dec.EffectiveEffort() == intent.EffortHigh
	}
}

func policyFor(dec intent.Decision, prof config.Profile, mode ThinkMode) agent.ThinkingPolicy {
	if mode == ThinkAlways {
		return agent.ThinkOn
	}
	if mode == ThinkNever {
		return agent.ThinkOff
	}
	switch dec.Intent {
	case intent.Build:
		return parseThink(prof.BuildThink(), agent.ThinkPlanStep)
	case intent.Act:
		return parseThink(prof.ActThinking(), agent.ThinkPlanStep)
	case intent.Lookup:
		return parseThink(prof.LookupThinking(), agent.ThinkLookup)
	default:
		if answerThink(&dec, prof.AnswerThinking(), ThinkAuto) {
			return agent.ThinkOn
		}
		return agent.ThinkOff
	}
}

func parseThink(policy string, fallback agent.ThinkingPolicy) agent.ThinkingPolicy {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "on":
		return agent.ThinkOn
	case "off":
		return agent.ThinkOff
	case "until_plan":
		return agent.ThinkUntilPlan
	case "plan_step":
		return agent.ThinkPlanStep
	case "auto", "":
		return fallback
	default:
		return agent.ParseBuildThinking(policy)
	}
}

func ThinkingFor(dec intent.Decision, buildThinking string, mode ThinkMode) agent.ThinkingPolicy {
	if mode == ThinkAlways {
		return agent.ThinkOn
	}
	if mode == ThinkNever {
		return agent.ThinkOff
	}
	switch dec.Intent {
	case intent.Build, intent.Act:
		return agent.ParseBuildThinking(buildThinking)
	case intent.Lookup:
		return agent.ThinkLookup
	case intent.Answer:
		if dec.EffectiveEffort() == intent.EffortHigh {
			return agent.ThinkOn
		}
		return agent.ThinkOff
	default:
		if dec.Reasoning {
			return agent.ThinkOn
		}
		return agent.ThinkOff
	}
}

const (
	maxShellKept  = 5
	maxShellBytes = 256 * 1024
)

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func (r *Runner) RememberShell(output string) {
	if r == nil {
		return
	}
	output = clipBytes(output, maxShellBytes)
	r.shellOut = append(r.shellOut, output)
	if len(r.shellOut) > maxShellKept {
		r.shellOut = append([]string(nil), r.shellOut[len(r.shellOut)-maxShellKept:]...)
	}
}

func (r *Runner) ShellOutput(n int) (string, bool) {
	if r == nil || n < 0 || n >= len(r.shellOut) {
		return "", false
	}
	return r.shellOut[len(r.shellOut)-1-n], true
}
