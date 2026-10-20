package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	urlUserinfo = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^:/?#\s]+:[^@/\s]+@`)
	userKV      = regexp.MustCompile(`(?i)\buser=\S+`)
	passKV      = regexp.MustCompile(`(?i)\bpassword=\S+`)
)

const builtinProfile = "default"

// Profile is the resolved config passed down through the process.
// Nested sections are the source of truth; flat fields stay populated
// so existing callers keep compiling.
type Profile = Config

type Config struct {
	Name string `yaml:"-" json:"-"`

	Models   map[string]Model `yaml:"models"`
	Roles    Roles            `yaml:"roles"`
	LLM      LLM              `yaml:"llm"`
	Fast     Fast             `yaml:"fast"`
	Database Database         `yaml:"database"`
	Memory   Memory           `yaml:"memory"`
	Context  Context          `yaml:"context"`
	Agent    Agent            `yaml:"agent"`
	RAG      RAG              `yaml:"rag"`
	UI       UI               `yaml:"ui"`
	Language Language         `yaml:"language"`
	Safety   Safety           `yaml:"safety"`
	Router   Router           `yaml:"router"`
	Tools    Tools            `yaml:"tools"`
	Store    Store            `yaml:"store"`
	Stats    Stats            `yaml:"stats"`
}

// Model is one named chat endpoint. Context 0 means the server reports the window.
type Model struct {
	Endpoint       string `yaml:"endpoint"`
	Model          string `yaml:"model"`
	ThinkingKwargs bool   `yaml:"thinking_kwargs"`
	Context        int    `yaml:"context"`
	Notes          string `yaml:"notes"`
}

// Roles selects named models and optional embed/rerank URLs.
type Roles struct {
	Main   string `yaml:"main"`
	Fast   string `yaml:"fast"`
	Embed  string `yaml:"embed"`
	Rerank string `yaml:"rerank"`
}

type LLM struct {
	IdleTimeoutSeconds int          `yaml:"idle_timeout_seconds"`
	MaxTokens          MaxTokens    `yaml:"max_tokens"`
	Temperature        Temperatures `yaml:"temperature"`
	TopP               float64      `yaml:"top_p"`
	Retries            int          `yaml:"retries"`
	RetryWaitMS        int          `yaml:"retry_wait_ms"`
}

// Temperatures are sampling temperatures per intent.
type Temperatures struct {
	Answer float64 `yaml:"answer"`
	Lookup float64 `yaml:"lookup"`
	Act    float64 `yaml:"act"`
	Build  float64 `yaml:"build"`
}

type MaxTokens struct {
	Answer     int `yaml:"answer"`
	AnswerLow  int `yaml:"answer_low"`
	AnswerHigh int `yaml:"answer_high"`
	Lookup     int `yaml:"lookup"`
	Act        int `yaml:"act"`
	Build      int `yaml:"build"`
}

type Fast struct {
	ContextGuardRatio float64 `yaml:"context_guard_ratio"`
}

type Database struct {
	URL string `yaml:"url"`
}

type Memory struct {
	Enabled            bool    `yaml:"enabled"`
	SessionIdleMinutes int     `yaml:"session_idle_minutes"`
	RetentionDays      int     `yaml:"retention_days"`
	CompactThreshold   float64 `yaml:"compact_threshold"`
	IsolatedTimeoutMS  int     `yaml:"isolated_timeout_ms"`
}

type Context struct {
	ElideTrigger float64 `yaml:"elide_trigger"`
	ElideTarget  float64 `yaml:"elide_target"`
}

type Agent struct {
	MaxSteps         Steps         `yaml:"max_steps"`
	ExploreBudget    ExploreBudget `yaml:"explore_budget"`
	BuildThinking    string        `yaml:"build_thinking"`
	ThinkMode        string        `yaml:"think_mode"`
	ThinkAnswer      string        `yaml:"think_answer"`
	ThinkLookup      string        `yaml:"think_lookup"`
	ThinkAct         string        `yaml:"think_act"`
	Approve          string        `yaml:"approve"`
	Plan             PlanGate      `yaml:"plan"`
	IdenticalRejects int           `yaml:"identical_rejects"`
}

// PlanGate says when a turn must propose a plan before it writes.
type PlanGate struct {
	ActLow  bool `yaml:"act_low"`
	ActHigh bool `yaml:"act_high"`
	Build   bool `yaml:"build"`
}

type Steps struct {
	Lookup int `yaml:"lookup"`
	Act    int `yaml:"act"`
	Build  int `yaml:"build"`
}

type ExploreBudget struct {
	Lookup         int `yaml:"lookup"`
	LookupAnalysis int `yaml:"lookup_analysis"`
	Act            int `yaml:"act"`
	Build          int `yaml:"build"`
	BuildDocs      int `yaml:"build_docs"`
	BuildAnalysis  int `yaml:"build_analysis"`
}

type RAG struct {
	Enabled    bool      `yaml:"enabled"`
	EmbedBatch int       `yaml:"embed_batch"`
	Chunk      RAGChunk  `yaml:"chunk"`
	Search     RAGSearch `yaml:"search"`
	Index      RAGIndex  `yaml:"index"`
}

type RAGChunk struct {
	TurnTokens          int `yaml:"turn_tokens"`
	WebTokens           int `yaml:"web_tokens"`
	WebOverlapTokens    int `yaml:"web_overlap_tokens"`
	CodeWindowLines     int `yaml:"code_window_lines"`
	CodeOverlapLines    int `yaml:"code_overlap_lines"`
	GoMaxDeclLines      int `yaml:"go_max_decl_lines"`
	GoSplitWindowLines  int `yaml:"go_split_window_lines"`
	GoSplitOverlapLines int `yaml:"go_split_overlap_lines"`
}

type RAGSearch struct {
	VectorTop       int     `yaml:"vector_top"`
	TextTop         int     `yaml:"text_top"`
	RRFK            int     `yaml:"rrf_k"`
	RerankTop       int     `yaml:"rerank_top"`
	K               int     `yaml:"k"`
	RerankThreshold float64 `yaml:"rerank_threshold"`
}

type RAGIndex struct {
	MaxFileKB          int `yaml:"max_file_kb"`
	ConfirmOverMinutes int `yaml:"confirm_over_minutes"`
}

type UI struct {
	DisplayLines        int      `yaml:"display_lines"`
	PasteChipLines      int      `yaml:"paste_chip_lines"`
	PasteChipChars      int      `yaml:"paste_chip_chars"`
	OutputKeepKB        int      `yaml:"output_keep_kb"`
	Banner              bool     `yaml:"banner"`
	Welcome             string   `yaml:"welcome"`
	Theme               string   `yaml:"theme"`
	Appearance          string   `yaml:"appearance"`
	CursorColor         bool     `yaml:"cursor_color"`
	CodeHighlight       bool     `yaml:"code_highlight"`
	ClearOnStart        bool     `yaml:"clear_on_start"`
	ClearHeader         string   `yaml:"clear_header"`
	PinnedHeader        bool     `yaml:"pinned_header"`
	StripEmoji          bool     `yaml:"strip_emoji"`
	ProbeDeadlineMS     int      `yaml:"probe_deadline_ms"`
	ProbeTimeoutMS      int      `yaml:"probe_timeout_ms"`
	EscTimeoutMS        int      `yaml:"esc_timeout_ms"`
	GitQueryMS          int      `yaml:"git_query_ms"`
	MemoryQueryMS       int      `yaml:"memory_query_ms"`
	StartupCapMS        int      `yaml:"startup_cap_ms"`
	WelcomeCacheSeconds int      `yaml:"welcome_cache_seconds"`
	ModelRateCalls      int      `yaml:"model_rate_calls"`
	ModelSlowMS         int      `yaml:"model_slow_ms"`
	CtxWarnPct          int      `yaml:"ctx_warn_pct"`
	CtxHotPct           int      `yaml:"ctx_hot_pct"`
	CtxCells            int      `yaml:"ctx_cells"`
	CtxNarrowCols       int      `yaml:"ctx_narrow_cols"`
	Layout              UILayout `yaml:"layout"`
	Trace               bool     `yaml:"trace"`
	Stats               bool     `yaml:"stats"`
	Badge               bool     `yaml:"badge"`
}

// UILayout is the welcome size cuts. Zero fields keep the built-in cuts.
type UILayout struct {
	TinyHeight   int `yaml:"tiny_height"`
	RecentHeight int `yaml:"recent_height"`
	TinyWidth    int `yaml:"tiny_width"`
	NarrowWidth  int `yaml:"narrow_width"`
	FrameWidth   int `yaml:"frame_width"`
}

type Language struct {
	Mode      string   `yaml:"mode"`
	Languages []string `yaml:"languages"`
	Default   string   `yaml:"default"`
}

type Safety struct {
	ExtraReadDeny    []string `yaml:"extra_read_deny"`
	ExtraCommandDeny []string `yaml:"extra_command_deny"`
}

type Router struct {
	ActionVerbs []string `yaml:"action_verbs"`
	Targets     []string `yaml:"targets"`
}

type Tools struct {
	Shell ToolShell `yaml:"shell"`
	Web   ToolWeb   `yaml:"web"`
}

type ToolWeb struct {
	Enabled bool `yaml:"enabled"`
	Results int  `yaml:"results"`
}

type ToolShell struct {
	DefaultTimeoutSec int `yaml:"default_timeout_sec"`
	MaxTimeoutSec     int `yaml:"max_timeout_sec"`
	WaitDelayMS       int `yaml:"wait_delay_ms"`
}

type Store struct {
	QueryTimeoutMS   int `yaml:"query_timeout_ms"`
	MigrateTimeoutMS int `yaml:"migrate_timeout_ms"`
}

type Stats struct {
	InsertTimeoutMS int `yaml:"insert_timeout_ms"`
}

func Builtin() Config {
	c := Config{Name: builtinProfile}
	ApplyKeyDefaults(&c)
	c.Models = map[string]Model{
		"default": {
			Endpoint: "http://localhost:8000/v1",
		},
	}
	if strings.TrimSpace(c.Roles.Main) == "" {
		c.Roles.Main = "default"
	}
	return c
}

func defaultActionVerbs() []string {
	return []string{
		"create", "write", "make", "add", "generate", "fix", "update", "edit",
		"change", "refactor", "rename", "delete", "remove", "install", "run",
		"build", "deploy", "commit", "move", "copy",
		"kill", "stop", "start", "restart", "test", "free", "close", "open", "launch",
		"crée", "créer", "écris", "écrire", "ajoute", "ajouter", "génère", "générer",
		"corrige", "corriger", "modifie", "modifier", "supprime", "supprimer",
		"installe", "installer", "lance", "lancer", "déplace", "renomme",
		"tue", "tuer", "arrête", "arrete", "arrêter", "arreter", "démarre", "demarre",
		"relance", "teste", "libère", "libere", "ferme",
	}
}

func defaultTargets() []string {
	return []string{
		"file", "folder", "directory", "project", "repo", "branch", "readme",
		"test", "function", "endpoint", "fichier", "dossier", "projet", "branche",
		"process", "pid", "port", "server", "container", "service",
		"processus", "serveur", "conteneur",
	}
}

func (c Config) FastOn() bool {
	m, ok := c.FastModel()
	return ok && strings.TrimSpace(m.Endpoint) != ""
}

// Main returns the chat model selected by roles.main.
func (c Config) Main() Model {
	name := strings.TrimSpace(c.Roles.Main)
	if name == "" {
		name = "default"
	}
	if c.Models == nil {
		return Model{}
	}
	return c.Models[name]
}

// MainEndpoint is the OpenAI-compatible base for the main role.
func (c Config) MainEndpoint() string { return c.Main().Endpoint }

// MainModelID is the model id for the main role. Empty uses the server default.
func (c Config) MainModelID() string { return c.Main().Model }

// MainThinking reports whether the main model sends enable_thinking.
func (c Config) MainThinking() bool { return c.Main().ThinkingKwargs }

// FastModel returns the fast role when it names a configured model.
func (c Config) FastModel() (Model, bool) {
	name := strings.TrimSpace(c.Roles.Fast)
	if name == "" || c.Models == nil {
		return Model{}, false
	}
	m, ok := c.Models[name]
	return m, ok
}

// FastEndpoint is empty when the fast role is unconfigured.
func (c Config) FastEndpoint() string {
	m, ok := c.FastModel()
	if !ok {
		return ""
	}
	return m.Endpoint
}

// FastModelID is the fast role's model id.
func (c Config) FastModelID() string {
	m, ok := c.FastModel()
	if !ok {
		return ""
	}
	return m.Model
}

// FastThinking reports enable_thinking for the fast role.
func (c Config) FastThinking() bool {
	m, ok := c.FastModel()
	return ok && m.ThinkingKwargs
}

// EmbedURL is roles.embed. Empty means embed is not configured.
func (c Config) EmbedURL() string { return strings.TrimSpace(c.Roles.Embed) }

// RerankURL is roles.rerank. Empty means rerank is not configured.
func (c Config) RerankURL() string { return strings.TrimSpace(c.Roles.Rerank) }

// ModelNames returns configured model names in sorted order.
func (c Config) ModelNames() []string {
	names := make([]string, 0, len(c.Models))
	for name := range c.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WithMain returns the built-in config with the main model endpoint and id set.
func WithMain(endpoint, model string) Config {
	c := Builtin()
	c.SetMain(endpoint, model, false)
	return c
}

// SetMain ensures roles.main names a model and sets its endpoint, id, and thinking flag.
func (c *Config) SetMain(endpoint, model string, think bool) {
	if c.Models == nil {
		c.Models = map[string]Model{}
	}
	name := strings.TrimSpace(c.Roles.Main)
	if name == "" {
		name = "default"
		c.Roles.Main = name
	}
	m := c.Models[name]
	m.Endpoint = endpoint
	m.Model = model
	m.ThinkingKwargs = think
	c.Models[name] = m
}

// SetFast ensures a model named name and points roles.fast at it.
// An empty endpoint clears the fast role.
func (c *Config) SetFast(name, endpoint, model string, think bool) {
	if strings.TrimSpace(endpoint) == "" {
		c.Roles.Fast = ""
		return
	}
	if c.Models == nil {
		c.Models = map[string]Model{}
	}
	if strings.TrimSpace(name) == "" {
		name = "fast"
	}
	m := c.Models[name]
	m.Endpoint = endpoint
	m.Model = model
	m.ThinkingKwargs = think
	c.Models[name] = m
	c.Roles.Fast = name
}

// TemperatureFor returns the sampling temperature for an intent.
// A zero profile keeps the temperatures used before the key existed.
func (c Config) TemperatureFor(intent string) float64 {
	var v float64
	fallback := 0.2
	switch intent {
	case "build":
		v = c.LLM.Temperature.Build
		fallback = 0.3
	case "lookup":
		v = c.LLM.Temperature.Lookup
	case "act":
		v = c.LLM.Temperature.Act
	default:
		v = c.LLM.Temperature.Answer
		fallback = 0.3
	}
	if v > 0 {
		return v
	}
	return fallback
}

// NeedPlan reports whether intent must pass the plan gate.
// A zero profile keeps today's rule: build always, act only at high effort.
func (c Config) NeedPlan(intent string, high bool) bool {
	if c.planUnset() {
		if intent == "build" {
			return true
		}
		return intent == "act" && high
	}
	switch intent {
	case "build":
		return c.Agent.Plan.Build
	case "act":
		if high {
			return c.Agent.Plan.ActHigh
		}
		return c.Agent.Plan.ActLow
	default:
		return false
	}
}

func (c Config) planUnset() bool {
	return !c.Agent.Plan.ActLow && !c.Agent.Plan.ActHigh && !c.Agent.Plan.Build
}

// ShowStatsLine is the end-of-turn stats line. An unset UI block keeps it on.
func (c Config) ShowStatsLine() bool {
	if c.UI.Stats {
		return true
	}
	return c.uiUnset()
}

// ShowBadge is the intent badge. An unset UI block keeps it on.
func (c Config) ShowBadge() bool {
	if c.UI.Badge {
		return true
	}
	return c.uiUnset()
}

func (c Config) uiUnset() bool {
	return c.UI.DisplayLines == 0 && strings.TrimSpace(c.UI.Theme) == "" && !c.UI.Banner
}

// WebOn reports whether web_fetch is offered. An unset tools block keeps it on.
func (c Config) WebOn() bool {
	if c.Tools.Web.Enabled {
		return true
	}
	return c.Tools.Shell.DefaultTimeoutSec == 0 && c.Tools.Web.Results == 0
}

// ActThinking is the act-intent thinking policy.
func (c Config) ActThinking() string {
	if s := strings.TrimSpace(c.Agent.ThinkAct); s != "" {
		return s
	}
	if s := strings.TrimSpace(c.Agent.BuildThinking); s != "" {
		return s
	}
	return "plan_step"
}

// BuildThink is the build-intent thinking policy.
func (c Config) BuildThink() string {
	if s := strings.TrimSpace(c.Agent.BuildThinking); s != "" {
		return s
	}
	return "plan_step"
}

// AnswerThinking is auto, on, or off. Empty means auto (high effort only).
func (c Config) AnswerThinking() string {
	s := strings.TrimSpace(c.Agent.ThinkAnswer)
	if s == "" {
		return "auto"
	}
	return s
}

// LookupThinking is auto (after two failures), on, off, plan_step, or until_plan.
func (c Config) LookupThinking() string {
	s := strings.TrimSpace(c.Agent.ThinkLookup)
	if s == "" {
		return "auto"
	}
	return s
}

// MemoryOn is false only when memory.enabled was set off.
// An unset memory block stays on so an empty URL is still the only switch.
func (c Config) MemoryOn() bool {
	if c.Memory.Enabled {
		return true
	}
	return c.Memory.SessionIdleMinutes == 0 && c.Memory.RetentionDays == 0
}

func (c Config) RAGEnabled() bool {
	return c.RAG.Enabled
}

// AnswerEffortCap is the answer max_tokens for low or high effort.
// None and unknown efforts return 0 so the caller keeps the existing answer cap.
func (c Config) AnswerEffortCap(effort string) int {
	switch effort {
	case "low":
		return nonzero(c.LLM.MaxTokens.AnswerLow, 600)
	case "high":
		return nonzero(c.LLM.MaxTokens.AnswerHigh, 2400)
	default:
		return 0
	}
}

func (c Config) TokenCap(intent string) int {
	switch intent {
	case "build":
		return nonzero(c.LLM.MaxTokens.Build, 12288)
	case "lookup":
		return nonzero(c.LLM.MaxTokens.Lookup, 4096)
	case "act":
		return nonzero(c.LLM.MaxTokens.Act, 4096)
	default:
		return nonzero(c.LLM.MaxTokens.Answer, 4096)
	}
}

func (c Config) MaxStepsFor(intent string) int {
	switch intent {
	case "lookup":
		return nonzero(c.Agent.MaxSteps.Lookup, 10)
	case "build":
		return nonzero(c.Agent.MaxSteps.Build, 60)
	default:
		return nonzero(c.Agent.MaxSteps.Act, 15)
	}
}

// IdenticalRejectLimit stops a turn after this many identical rejected tool calls.
func (c Config) IdenticalRejectLimit() int {
	return nonzero(c.Agent.IdenticalRejects, 2)
}

func (c Config) ExploreBudgetFor(intent, task string) int {
	b := c.Agent.ExploreBudget
	switch intent {
	case "lookup":
		if task == "analysis" {
			return nonzero(b.LookupAnalysis, 10)
		}
		return nonzero(b.Lookup, 6)
	case "build":
		switch task {
		case "docs":
			return nonzero(b.BuildDocs, 16)
		case "analysis":
			return nonzero(b.BuildAnalysis, 12)
		}
		return nonzero(b.Build, 8)
	default:
		return nonzero(b.Act, 6)
	}
}

func (c Config) ElideTrigger() float64 {
	if c.Context.ElideTrigger > 0 {
		return c.Context.ElideTrigger
	}
	return 0.60
}

func (c Config) ElideTarget() float64 {
	if c.Context.ElideTarget > 0 {
		return c.Context.ElideTarget
	}
	return 0.40
}

func (c Config) CompactThreshold() float64 {
	if c.Memory.CompactThreshold > 0 {
		return c.Memory.CompactThreshold
	}
	return 0.7
}

func (c Config) FastGuardRatio() float64 {
	if c.Fast.ContextGuardRatio > 0 {
		return c.Fast.ContextGuardRatio
	}
	return 0.60
}

func (c Config) DisplayLines() int {
	return nonzero(c.UI.DisplayLines, 60)
}

func (c Config) PasteChipLines() int {
	return nonzero(c.UI.PasteChipLines, 11)
}

func (c Config) PasteChipChars() int {
	return nonzero(c.UI.PasteChipChars, 1000)
}

func (c Config) OutputKeepBytes() int {
	return nonzero(c.UI.OutputKeepKB, 256) * 1024
}

func (c Config) PaletteName() string {
	if name, ok := ResolveTheme(c.UI.Theme); ok {
		return name
	}
	return "amber"
}

func (c Config) ShowBanner() bool {
	return c.UI.Banner
}

// ProbeDeadline is the welcome gather budget. Omitted config keeps 400ms.
func (c Config) ProbeDeadline() time.Duration {
	return time.Duration(nonzero(c.UI.ProbeDeadlineMS, 400)) * time.Millisecond
}

// ProbeTimeout is one welcome endpoint probe. Omitted config keeps 300ms.
func (c Config) ProbeTimeout() time.Duration {
	return time.Duration(nonzero(c.UI.ProbeTimeoutMS, 300)) * time.Millisecond
}

// EscTimeout is the gap allowed between bytes of one escape sequence.
// Omitted config keeps 30ms. A lone Esc is the sequence that never continues.
func (c Config) EscTimeout() time.Duration {
	return time.Duration(nonzero(c.UI.EscTimeoutMS, 30)) * time.Millisecond
}

// GitQueryTimeout bounds git status checks. Omitted config keeps 200ms.
func (c Config) GitQueryTimeout() time.Duration {
	return time.Duration(nonzero(c.UI.GitQueryMS, 200)) * time.Millisecond
}

// MemoryQueryTimeout bounds the welcome memory query. Omitted config keeps 200ms.
func (c Config) MemoryQueryTimeout() time.Duration {
	return time.Duration(nonzero(c.UI.MemoryQueryMS, 200)) * time.Millisecond
}

// StartupCap is the longest startup wait before the prompt. Omitted config keeps 500ms.
func (c Config) StartupCap() time.Duration {
	return time.Duration(nonzero(c.UI.StartupCapMS, 500)) * time.Millisecond
}

// WelcomeCacheFor is how long a painted welcome stays fresh. Omitted config keeps 60s.
func (c Config) WelcomeCacheFor() time.Duration {
	return time.Duration(nonzero(c.UI.WelcomeCacheSeconds, 60)) * time.Second
}

// ModelRateWindow is how many recent calls feed tokens/s. Omitted config keeps 20.
func (c Config) ModelRateWindow() int {
	return nonzero(c.UI.ModelRateCalls, 20)
}

// ModelSlow is the welcome latency that counts as slow. Omitted config keeps 1s.
func (c Config) ModelSlow() time.Duration {
	return time.Duration(nonzero(c.UI.ModelSlowMS, 1000)) * time.Millisecond
}

// ShellDefaultTimeout is run_shell's timeout when the model omits one. Omitted config keeps 120s.
func (c Config) ShellDefaultTimeout() time.Duration {
	return time.Duration(nonzero(c.Tools.Shell.DefaultTimeoutSec, 120)) * time.Second
}

// ShellMaxTimeout is the longest run_shell timeout. Omitted config keeps 600s.
func (c Config) ShellMaxTimeout() time.Duration {
	return time.Duration(nonzero(c.Tools.Shell.MaxTimeoutSec, 600)) * time.Second
}

// ShellWaitDelay is how long a cancelled shell may take to exit. Omitted config keeps 2s.
func (c Config) ShellWaitDelay() time.Duration {
	return time.Duration(nonzero(c.Tools.Shell.WaitDelayMS, 2000)) * time.Millisecond
}

// StoreQueryTimeout bounds a database call. Omitted config keeps 2s.
func (c Config) StoreQueryTimeout() time.Duration {
	return time.Duration(nonzero(c.Store.QueryTimeoutMS, 2000)) * time.Millisecond
}

// StoreMigrateTimeout bounds schema migration. Omitted config keeps 15s.
func (c Config) StoreMigrateTimeout() time.Duration {
	return time.Duration(nonzero(c.Store.MigrateTimeoutMS, 15000)) * time.Millisecond
}

// IsolatedTimeout bounds a memory call detached from the turn. Omitted config keeps 2s.
func (c Config) IsolatedTimeout() time.Duration {
	return time.Duration(nonzero(c.Memory.IsolatedTimeoutMS, 2000)) * time.Millisecond
}

// StatsInsertTimeout bounds a turn-stat insert. Omitted config keeps 2s.
func (c Config) StatsInsertTimeout() time.Duration {
	return time.Duration(nonzero(c.Stats.InsertTimeoutMS, 2000)) * time.Millisecond
}

func (c Config) CtxWarnPct() int { return nonzero(c.UI.CtxWarnPct, 60) }
func (c Config) CtxHotPct() int  { return nonzero(c.UI.CtxHotPct, 85) }
func (c Config) CtxCells() int   { return nonzero(c.UI.CtxCells, 8) }
func (c Config) CtxNarrowCols() int {
	return nonzero(c.UI.CtxNarrowCols, 90)
}

func (c Config) LayoutTinyHeight() int   { return nonzero(c.UI.Layout.TinyHeight, 22) }
func (c Config) LayoutRecentHeight() int { return nonzero(c.UI.Layout.RecentHeight, 30) }
func (c Config) LayoutTinyWidth() int    { return nonzero(c.UI.Layout.TinyWidth, 64) }
func (c Config) LayoutNarrowWidth() int  { return nonzero(c.UI.Layout.NarrowWidth, 110) }
func (c Config) LayoutFrameWidth() int   { return nonzero(c.UI.Layout.FrameWidth, 120) }

// WelcomeLayout is ui.welcome: split, frame, or minimal. Anything else is split.
func (c Config) WelcomeLayout() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.Welcome)) {
	case "frame", "minimal", "split":
		return strings.ToLower(strings.TrimSpace(c.UI.Welcome))
	default:
		return "split"
	}
}

func (c Config) Appearance() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.Appearance)) {
	case "light", "dark":
		return strings.ToLower(strings.TrimSpace(c.UI.Appearance))
	default:
		return "auto"
	}
}

func (c Config) CursorColorOn() bool {
	return c.UI.CursorColor
}

func (c Config) CodeHighlightOn() bool {
	return c.UI.CodeHighlight
}

func (c Config) ClearOnStart() bool {
	return c.UI.ClearOnStart
}

// ClearHeader is ui.clear_header: full, compact, or none. Anything else is full.
func (c Config) ClearHeader() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.ClearHeader)) {
	case "compact", "none", "full":
		return strings.ToLower(strings.TrimSpace(c.UI.ClearHeader))
	default:
		return "full"
	}
}

func (c Config) PinnedHeader() bool {
	return c.UI.PinnedHeader
}

func (c Config) StripEmojiOn() bool {
	return c.UI.StripEmoji
}

func nonzero(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}

func DefaultPath() string {
	return filepath.Join(configDir(), "config.yaml")
}

func JSONPath() string {
	return filepath.Join(configDir(), "config.json")
}

func configDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "konsolai")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "konsolai")
	}
	return filepath.Join(home, ".config", "konsolai")
}

func (p Config) Location() string {
	host, db := hostDB(p.Database.URL)
	switch {
	case host != "" && db != "":
		return host + "/" + db
	case host != "":
		return host
	default:
		return db
	}
}

func DisableLine(databaseURL string, err error) string {
	host, _ := hostDB(databaseURL)
	if host == "" {
		host = "database"
	}
	cause := innermostCause(err)
	if cause == "" {
		cause = "unknown error"
	}
	return "memory disabled: cannot connect to " + host + ": " + cause
}

func innermostCause(err error) string {
	if err == nil {
		return ""
	}
	var op *net.OpError
	if errors.As(err, &op) {
		if op.Err != nil {
			return stripConnectSecrets(firstLine(op.Err.Error()))
		}
		return stripConnectSecrets(firstLine(op.Error()))
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		if inner := errors.Unwrap(ce); inner != nil {
			return innermostCause(inner)
		}
		return stripConnectSecrets(firstLine(ce.Error()))
	}
	for {
		next := errors.Unwrap(err)
		if next == nil {
			break
		}
		err = next
	}
	return stripConnectSecrets(firstLine(err.Error()))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func stripConnectSecrets(s string) string {
	s = urlUserinfo.ReplaceAllString(s, "://")
	s = userKV.ReplaceAllString(s, "")
	s = passKV.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func hostDB(raw string) (host, dbname string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", ""
		}
		return u.Host, strings.TrimPrefix(u.Path, "/")
	}
	for _, field := range strings.Fields(raw) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch k {
		case "host":
			host = v
		case "dbname":
			dbname = v
		}
	}
	return host, dbname
}

func MaskURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		if u.User != nil {
			if _, ok := u.User.Password(); ok {
				user := u.User.Username()
				host := u.Host
				rest := ""
				if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
					rest = u.RequestURI()
				}
				scheme := u.Scheme
				if user != "" {
					return scheme + "://" + user + ":***@" + host + rest
				}
				return scheme + "://***@" + host + rest
			}
		}
		return u.String()
	}
	parts := strings.Fields(raw)
	for i, field := range parts {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(k, "password") && v != "" {
			parts[i] = k + "=***"
		}
	}
	return strings.Join(parts, " ")
}
