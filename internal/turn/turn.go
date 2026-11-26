package turn

import (
	"context"
	"fmt"
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
	"github.com/SniksaX/Konsolai/internal/sysctx"
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
	Serving          agent.Model
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
	MCPRuntime       *MCPRuntime
	// MCPPrivateConfirm asks once per process before a remote main model sees a
	// private MCP server. Nil refuses by default; --yes skips the prompt.
	MCPPrivateConfirm func(server string) (bool, error)
	shellOut          []string
	lastCode          int
	lastIntent        intent.Intent
	lastLang          string
	lastEffort        intent.Effort
	lastTask          intent.Task
	lastUser          string
	lastAssistant     string
	lastPromise       bool
	lastEscalate      intent.Intent
	answerCut         bool
	escalated         bool
	RebindNext        bool
	remoteYes         bool
	statMu            sync.Mutex
	statOn            bool
	statIntent        string
	statSession       string
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

// setRateLimitNotice prints the single 429 wait line on the turn's stderr.
func (r *Runner) setRateLimitNotice(c *llm.Client) {
	if c == nil {
		return
	}
	c.SetRateLimitHandlers(nil, func(n int) {
		fmt.Fprintf(r.errw(), "rate limited, retrying in %ds\n", n)
	})
}

func (r *Runner) client() *llm.Client {
	if r.Client == nil {
		c := llm.New(r.Profile.MainEndpoint(), r.Profile.MainModelID())
		applyModelOptions(c, r.Profile, r.Profile.Roles.Main)
		c.SetIdleTimeout(time.Duration(r.Profile.LLM.IdleTimeoutSeconds) * time.Second)
		c.SetThinkKwargs(r.Profile.MainThinking())
		if n := r.Profile.Main().Context; n > 0 {
			c.PinContext(n)
		}
		c.SetRetry(r.Profile.LLM.Retries, time.Duration(r.Profile.LLM.RetryWaitMS)*time.Millisecond)
		r.setRateLimitNotice(c)
		r.Client = c
	}
	applyRemotePolicy(r.Client, r.Profile, "main", r.Profile.Roles.Main, r.remoteYes)
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
	applyModelOptions(c, r.Profile, r.Profile.Roles.Fast)
	c.SetIdleTimeout(time.Duration(r.Profile.LLM.IdleTimeoutSeconds) * time.Second)
	c.SetThinkKwargs(r.Profile.FastThinking())
	if fm, ok := r.Profile.FastModel(); ok && fm.Context > 0 {
		c.PinContext(fm.Context)
	}
	c.SetRetry(r.Profile.LLM.Retries, time.Duration(r.Profile.LLM.RetryWaitMS)*time.Millisecond)
	r.setRateLimitNotice(c)
	r.FastClient = c
	applyRemotePolicy(c, r.Profile, "fast", r.Profile.Roles.Fast, r.remoteYes)
	r.attachStats(c)
	return c
}

// applyModelOptions copies one model's auth and request quirks onto a client.
func applyModelOptions(c *llm.Client, prof config.Profile, name string) {
	if c == nil {
		return
	}
	m, ok := prof.Models[name]
	if !ok {
		return
	}
	c.SetAPIKey(prof.APIKeyResolver(name))
	c.SetModelName(name)
	c.SetMaxTokensField(m.TokensField())
	c.SetSendTemperature(m.SendsTemperature())
	c.SetRemoteProbeTimeout(prof.RemoteProbeTimeout())
	c.SetThinkingMode(m.ChatThinking())
	c.SetReasoningEffort(m.ChatReasoningEffort())
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

// applyRemotePolicy gates a remote client and feeds its usage into the cap.
func applyRemotePolicy(c *llm.Client, prof config.Profile, role, name string, yes bool) {
	if c == nil {
		return
	}
	m, ok := prof.Models[name]
	if !ok || !m.Remote() {
		return
	}
	if !prof.Privacy.AllowRemote {
		host := config.URLHost(m.Endpoint)
		c.SetPolicy(func() error {
			return fmt.Errorf("config: privacy.allow_remote is false; model %q is remote (%s) and cannot be used", name, host)
		})
		return
	}
	if !prof.RemoteRoleAllowed(role) {
		host := config.URLHost(m.Endpoint)
		c.SetPolicy(func() error {
			return fmt.Errorf("config: privacy.remote_roles does not allow role %q to use remote model %q (%s)", role, name, host)
		})
		return
	}
	if strings.TrimSpace(m.Model) == "" {
		// A remote model needs an explicit id: the confirmation prompt is
		// pointless when the turn would fail right after it, because a remote
		// probe cannot pick the id for the user.
		c.SetPolicy(func() error { return remoteModelIDError(name) })
		return
	}
	c.SetPolicy(prof.RemoteCallPolicy(role, name, yes))
	c.SetUsageHook(func(u llm.Usage) { config.NoteRemoteUsage(u.PromptTokens + u.CompletionTokens) })
}

// remoteModelIDError is the actionable failure for a remote model with no id.
func remoteModelIDError(name string) error {
	return fmt.Errorf("llm: remote model %q has no model id; run: /set model id %s <model-id>", name, name)
}

// turnPricing is the serving model's cost inputs. mainServed is false when a
// fast answer carried the turn.
func (r *Runner) turnPricing(mainServed bool) agent.Pricing {
	name := strings.TrimSpace(r.Profile.Roles.Main)
	if !mainServed {
		name = strings.TrimSpace(r.Profile.Roles.Fast)
	}
	m, ok := r.Profile.Models[name]
	if !ok || !m.Remote() {
		return agent.Pricing{}
	}
	return agent.Pricing{Remote: true, PriceIn: m.PriceIn, PriceOut: m.PriceOut}
}

// modelFacts describes the configured models and the role answering this call.
// It is the single source of the identity block in every system context.
func (r *Runner) modelFacts(role string) sysctx.ModelFacts {
	c := r.Profile
	mainName := strings.TrimSpace(c.Roles.Main)
	if mainName == "" {
		mainName = "default"
	}
	facts := sysctx.ModelFacts{
		MainName:   mainName,
		MainID:     c.MainModelID(),
		MainHost:   EndpointHost(c.MainEndpoint()),
		MainRemote: c.ModelRemote(mainName),
		Role:       role,
	}
	if c.FastOn() {
		fastName := strings.TrimSpace(c.Roles.Fast)
		facts.FastName = fastName
		facts.FastID = c.FastModelID()
		facts.FastHost = EndpointHost(c.FastEndpoint())
		facts.FastRemote = c.ModelRemote(fastName)
		facts.HasFast = true
	}
	return facts
}

// servingModel is the attribution of one call: the configured model name, the
// id the client resolved (else its configured id), the role, and its locality.
// role is main, fast, or local for an answer built from the configured facts.
func (r *Runner) servingModel(ctx context.Context, client *llm.Client, role string) agent.Model {
	c := r.Profile
	name := strings.TrimSpace(c.Roles.Main)
	switch role {
	case "fast":
		name = strings.TrimSpace(c.Roles.Fast)
	case "local":
		return agent.Model{Name: "facts", Role: "local"}
	}
	if name == "" {
		name = "default"
	}
	id := ""
	// Never probe a remote host just to label a call: the attribution must not
	// trigger the confirmation or a request. Use the configured id.
	remote := c.ModelRemote(name)
	if client != nil {
		if !remote {
			if got, err := client.ResolveModel(ctx); err == nil {
				id = got
			}
		}
		if id == "" {
			id = client.ModelID()
		}
	}
	if id == "" {
		if m, ok := c.Models[name]; ok {
			id = m.Model
		}
	}
	return agent.Model{Name: name, ID: id, Role: role, Remote: c.ModelRemote(name)}
}
