package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Kind is the value stored for one leaf key.
type Kind int

const (
	KindString Kind = iota
	KindBool
	KindInt
	KindFloat
	KindDurationMS
	KindList
	KindEnum
)

// Apply says when a /set change takes effect.
type Apply int

const (
	ApplyNow Apply = iota
	ApplyNext
	ApplyRestart
)

// Check is the per-key constraint generated into validation.
type Check int

const (
	CheckNone Check = iota
	CheckPositive
	CheckRatio
	CheckRatioClosed
	CheckURL
	CheckURLOptional
	CheckDBURL
	CheckTheme
	CheckAppearance
	CheckWarnPct
	CheckTemp
	CheckTopP
	CheckNonNeg
)

// Key is one leaf of the config. The table in this file is the only declaration.
type Key struct {
	Path     string
	Kind     Kind
	Doc      string
	Env      string
	Secret   bool
	Aliases  []string
	Name     string
	Apply    Apply
	Allowed  []string
	Check    Check
	Str      string
	Bool     bool
	Int      int
	Float    float64
	List     []string
	SetStr   func(*Config, string)
	GetStr   func(Config) string
	SetBool  func(*Config, bool)
	GetBool  func(Config) bool
	SetInt   func(*Config, int)
	GetInt   func(Config) int
	SetFloat func(*Config, float64)
	GetFloat func(Config) float64
	SetList  func(*Config, []string)
	GetList  func(Config) []string
}

func (k Key) chk(c Check) Key { k.Check = c; return k }
func (k Key) env(name string) Key {
	k.Env = name
	return k
}
func (k Key) sec() Key {
	k.Secret = true
	return k
}
func (k Key) alias(paths ...string) Key {
	k.Aliases = append([]string(nil), paths...)
	return k
}
func (k Key) named(name string) Key {
	k.Name = name
	return k
}
func (k Key) when(a Apply) Key {
	k.Apply = a
	return k
}
func (k Key) allowed(v ...string) Key {
	k.Allowed = append([]string(nil), v...)
	if k.Kind == KindString {
		k.Kind = KindEnum
	}
	return k
}

func ks(path, doc, env, def string, set func(*Config, string), get func(Config) string) Key {
	return Key{Path: path, Kind: KindString, Doc: doc, Env: env, Str: def, SetStr: set, GetStr: get}
}

func kb(path, doc string, def bool, set func(*Config, bool), get func(Config) bool) Key {
	return Key{Path: path, Kind: KindBool, Doc: doc, Bool: def, SetBool: set, GetBool: get}
}

func ki(path, doc string, def int, set func(*Config, int), get func(Config) int) Key {
	return Key{Path: path, Kind: KindInt, Doc: doc, Int: def, Check: CheckPositive, SetInt: set, GetInt: get}
}

func km(path, doc string, def int, set func(*Config, int), get func(Config) int) Key {
	k := ki(path, doc, def, set, get)
	k.Kind = KindDurationMS
	return k
}

func kf(path, doc string, def float64, set func(*Config, float64), get func(Config) float64) Key {
	return Key{Path: path, Kind: KindFloat, Doc: doc, Float: def, Check: CheckRatio, SetFloat: set, GetFloat: get}
}

func kli(path, doc string, def []string, set func(*Config, []string), get func(Config) []string) Key {
	return Key{Path: path, Kind: KindList, Doc: doc, List: copyList(def), SetList: set, GetList: get}
}

func ktemp(path, doc string, def float64, set func(*Config, float64), get func(Config) float64) Key {
	return Key{Path: path, Kind: KindFloat, Doc: doc, Float: def, Check: CheckTemp, SetFloat: set, GetFloat: get}
}

func knn(path, doc string, def int, set func(*Config, int), get func(Config) int) Key {
	return Key{Path: path, Kind: KindInt, Doc: doc, Int: def, Check: CheckNonNeg, SetInt: set, GetInt: get}
}

func copyList(v []string) []string {
	out := make([]string, len(v))
	copy(out, v)
	return out
}

var keyTable = []Key{
	ks("roles.main", "name of the main chat model", "", "default",
		func(c *Config, v string) { c.Roles.Main = v }, func(c Config) string { return c.Roles.Main }).named("model").when(ApplyNext),
	ks("roles.fast", "name of the fast model, or empty", "", "",
		func(c *Config, v string) { c.Roles.Fast = v }, func(c Config) string { return c.Roles.Fast }).when(ApplyNext),
	ks("roles.embed", "embedding service URL, or empty", "", "",
		func(c *Config, v string) { c.Roles.Embed = v }, func(c Config) string { return c.Roles.Embed }).chk(CheckURLOptional).named("embed").when(ApplyNext),
	ks("roles.rerank", "rerank service URL, or empty", "", "",
		func(c *Config, v string) { c.Roles.Rerank = v }, func(c Config) string { return c.Roles.Rerank }).chk(CheckURLOptional).named("rerank").when(ApplyNext),

	ki("llm.idle_timeout_seconds", "stream idle limit", 120,
		func(c *Config, v int) { c.LLM.IdleTimeoutSeconds = v }, func(c Config) int { return c.LLM.IdleTimeoutSeconds }).named("timeout").when(ApplyNext),
	ki("llm.max_tokens.answer", "answer intent token cap", 4096,
		func(c *Config, v int) { c.LLM.MaxTokens.Answer = v }, func(c Config) int { return c.LLM.MaxTokens.Answer }),
	ki("llm.max_tokens.answer_low", "answer token cap at low effort", 600,
		func(c *Config, v int) { c.LLM.MaxTokens.AnswerLow = v }, func(c Config) int { return c.LLM.MaxTokens.AnswerLow }).named("answer.max_tokens.low"),
	ki("llm.max_tokens.answer_high", "answer token cap at high effort", 2400,
		func(c *Config, v int) { c.LLM.MaxTokens.AnswerHigh = v }, func(c Config) int { return c.LLM.MaxTokens.AnswerHigh }).named("answer.max_tokens.high"),
	ki("llm.max_tokens.lookup", "lookup intent token cap", 4096,
		func(c *Config, v int) { c.LLM.MaxTokens.Lookup = v }, func(c Config) int { return c.LLM.MaxTokens.Lookup }),
	ki("llm.max_tokens.act", "act intent token cap", 4096,
		func(c *Config, v int) { c.LLM.MaxTokens.Act = v }, func(c Config) int { return c.LLM.MaxTokens.Act }),
	ki("llm.max_tokens.build", "build intent token cap", 12288,
		func(c *Config, v int) { c.LLM.MaxTokens.Build = v }, func(c Config) int { return c.LLM.MaxTokens.Build }),
	ktemp("llm.temperature.answer", "answer sampling temperature", 0.3,
		func(c *Config, v float64) { c.LLM.Temperature.Answer = v }, func(c Config) float64 { return c.LLM.Temperature.Answer }).named("temperature").when(ApplyNext),
	ktemp("llm.temperature.lookup", "lookup sampling temperature", 0.2,
		func(c *Config, v float64) { c.LLM.Temperature.Lookup = v }, func(c Config) float64 { return c.LLM.Temperature.Lookup }).when(ApplyNext),
	ktemp("llm.temperature.act", "act sampling temperature", 0.2,
		func(c *Config, v float64) { c.LLM.Temperature.Act = v }, func(c Config) float64 { return c.LLM.Temperature.Act }).when(ApplyNext),
	ktemp("llm.temperature.build", "build sampling temperature", 0.3,
		func(c *Config, v float64) { c.LLM.Temperature.Build = v }, func(c Config) float64 { return c.LLM.Temperature.Build }).when(ApplyNext),
	kf("llm.top_p", "nucleus sampling; 0 omits the field", 0,
		func(c *Config, v float64) { c.LLM.TopP = v }, func(c Config) float64 { return c.LLM.TopP }).chk(CheckTopP).named("top_p").when(ApplyNext),
	ki("llm.retries", "extra attempts after a retriable model error", 1,
		func(c *Config, v int) { c.LLM.Retries = v }, func(c Config) int { return c.LLM.Retries }).when(ApplyNext),
	km("llm.retry_wait_ms", "pause before a model retry", 2000,
		func(c *Config, v int) { c.LLM.RetryWaitMS = v }, func(c Config) int { return c.LLM.RetryWaitMS }).when(ApplyNext),

	kf("fast.context_guard_ratio", "skip fast when the prompt fills past this fraction of its window", 0.60,
		func(c *Config, v float64) { c.Fast.ContextGuardRatio = v }, func(c Config) float64 { return c.Fast.ContextGuardRatio }),

	ks("database.url", "Postgres URL; empty disables memory", "KONSOLAI_DATABASE_URL", "",
		func(c *Config, v string) { c.Database.URL = v }, func(c Config) string { return c.Database.URL }).chk(CheckDBURL).sec().named("database").when(ApplyRestart),

	kb("memory.enabled", "use the database when a URL is set", true,
		func(c *Config, v bool) { c.Memory.Enabled = v }, func(c Config) bool { return c.Memory.Enabled }).named("memory").when(ApplyRestart),
	ki("memory.session_idle_minutes", "resume a session newer than this", 30,
		func(c *Config, v int) { c.Memory.SessionIdleMinutes = v }, func(c Config) int { return c.Memory.SessionIdleMinutes }).named("idle").when(ApplyNext),
	ki("memory.retention_days", "prune facts older than this", 90,
		func(c *Config, v int) { c.Memory.RetentionDays = v }, func(c Config) int { return c.Memory.RetentionDays }).named("retention").when(ApplyNext),
	kf("memory.compact_threshold", "compact history past this fraction of the window", 0.7,
		func(c *Config, v float64) { c.Memory.CompactThreshold = v }, func(c Config) float64 { return c.Memory.CompactThreshold }),
	km("memory.isolated_timeout_ms", "memory calls detached from the turn", 2000,
		func(c *Config, v int) { c.Memory.IsolatedTimeoutMS = v }, func(c Config) int { return c.Memory.IsolatedTimeoutMS }),

	kf("context.elide_trigger", "start eliding tool output above this fraction", 0.60,
		func(c *Config, v float64) { c.Context.ElideTrigger = v }, func(c Config) float64 { return c.Context.ElideTrigger }).named("elide").when(ApplyNext),
	kf("context.elide_target", "elide down toward this fraction; must stay below the trigger", 0.40,
		func(c *Config, v float64) { c.Context.ElideTarget = v }, func(c Config) float64 { return c.Context.ElideTarget }).when(ApplyNext),

	ki("agent.max_steps.lookup", "lookup turn step cap", 10,
		func(c *Config, v int) { c.Agent.MaxSteps.Lookup = v }, func(c Config) int { return c.Agent.MaxSteps.Lookup }),
	ki("agent.max_steps.act", "act turn step cap", 15,
		func(c *Config, v int) { c.Agent.MaxSteps.Act = v }, func(c Config) int { return c.Agent.MaxSteps.Act }).named("steps").when(ApplyNext),
	ki("agent.max_steps.build", "build turn step cap", 60,
		func(c *Config, v int) { c.Agent.MaxSteps.Build = v }, func(c Config) int { return c.Agent.MaxSteps.Build }),
	ki("agent.explore_budget.lookup", "read-only steps before the lookup nudge", 6,
		func(c *Config, v int) { c.Agent.ExploreBudget.Lookup = v }, func(c Config) int { return c.Agent.ExploreBudget.Lookup }).named("budget").when(ApplyNext),
	ki("agent.explore_budget.lookup_analysis", "lookup analysis read budget", 10,
		func(c *Config, v int) { c.Agent.ExploreBudget.LookupAnalysis = v }, func(c Config) int { return c.Agent.ExploreBudget.LookupAnalysis }),
	ki("agent.explore_budget.act", "act read budget before the nudge", 6,
		func(c *Config, v int) { c.Agent.ExploreBudget.Act = v }, func(c Config) int { return c.Agent.ExploreBudget.Act }),
	ki("agent.explore_budget.build", "build read budget before the plan nudge", 8,
		func(c *Config, v int) { c.Agent.ExploreBudget.Build = v }, func(c Config) int { return c.Agent.ExploreBudget.Build }),
	ki("agent.explore_budget.build_docs", "docs-task read budget", 16,
		func(c *Config, v int) { c.Agent.ExploreBudget.BuildDocs = v }, func(c Config) int { return c.Agent.ExploreBudget.BuildDocs }),
	ki("agent.explore_budget.build_analysis", "build analysis read budget", 12,
		func(c *Config, v int) { c.Agent.ExploreBudget.BuildAnalysis = v }, func(c Config) int { return c.Agent.ExploreBudget.BuildAnalysis }),
	ks("agent.build_thinking", "plan_step | until_plan", "KONSOLAI_BUILD_THINKING", "plan_step",
		func(c *Config, v string) { c.Agent.BuildThinking = v }, func(c Config) string { return c.Agent.BuildThinking }).allowed("plan_step", "until_plan").when(ApplyNext),
	ks("agent.think_mode", "auto | on | off", "", "auto",
		func(c *Config, v string) { c.Agent.ThinkMode = v }, func(c Config) string { return c.Agent.ThinkMode }).allowed("auto", "on", "off").named("think").when(ApplyNow),
	ks("agent.think_answer", "auto | on | off", "", "auto",
		func(c *Config, v string) { c.Agent.ThinkAnswer = v }, func(c Config) string { return c.Agent.ThinkAnswer }).allowed("auto", "on", "off").when(ApplyNext),
	ks("agent.think_lookup", "auto | on | off | plan_step | until_plan", "", "auto",
		func(c *Config, v string) { c.Agent.ThinkLookup = v }, func(c Config) string { return c.Agent.ThinkLookup }).allowed("auto", "on", "off", "plan_step", "until_plan").when(ApplyNext),
	ks("agent.think_act", "on | off | plan_step | until_plan", "", "plan_step",
		func(c *Config, v string) { c.Agent.ThinkAct = v }, func(c Config) string { return c.Agent.ThinkAct }).allowed("on", "off", "plan_step", "until_plan").when(ApplyNext),
	ks("agent.approve", "ask | in_plan | all_safe", "", "ask",
		func(c *Config, v string) { c.Agent.Approve = v }, func(c Config) string { return c.Agent.Approve }).allowed("ask", "in_plan", "all_safe").named("approve").when(ApplyNow),
	kb("agent.plan.act_low", "require a plan for a low-effort act turn", false,
		func(c *Config, v bool) { c.Agent.Plan.ActLow = v }, func(c Config) bool { return c.Agent.Plan.ActLow }).named("plan").when(ApplyNext),
	kb("agent.plan.act_high", "require a plan for a high-effort act turn", true,
		func(c *Config, v bool) { c.Agent.Plan.ActHigh = v }, func(c Config) bool { return c.Agent.Plan.ActHigh }).when(ApplyNext),
	kb("agent.plan.build", "require a plan for a build turn", true,
		func(c *Config, v bool) { c.Agent.Plan.Build = v }, func(c Config) bool { return c.Agent.Plan.Build }).when(ApplyNext),
	ki("agent.identical_rejects", "stop after this many identical rejected tool calls", 2,
		func(c *Config, v int) { c.Agent.IdenticalRejects = v }, func(c Config) int { return c.Agent.IdenticalRejects }),

	kb("rag.enabled", "index and search project memory", false,
		func(c *Config, v bool) { c.RAG.Enabled = v }, func(c Config) bool { return c.RAG.Enabled }).named("rag").when(ApplyNext),
	ki("rag.embed_batch", "texts per embed request", 32,
		func(c *Config, v int) { c.RAG.EmbedBatch = v }, func(c Config) int { return c.RAG.EmbedBatch }),
	ki("rag.chunk.turn_tokens", "tokens per stored turn chunk", 1200,
		func(c *Config, v int) { c.RAG.Chunk.TurnTokens = v }, func(c Config) int { return c.RAG.Chunk.TurnTokens }),
	ki("rag.chunk.web_tokens", "tokens per fetched-page chunk", 600,
		func(c *Config, v int) { c.RAG.Chunk.WebTokens = v }, func(c Config) int { return c.RAG.Chunk.WebTokens }),
	ki("rag.chunk.web_overlap_tokens", "overlap between web chunks", 80,
		func(c *Config, v int) { c.RAG.Chunk.WebOverlapTokens = v }, func(c Config) int { return c.RAG.Chunk.WebOverlapTokens }),
	ki("rag.chunk.code_window_lines", "lines per code chunk", 60,
		func(c *Config, v int) { c.RAG.Chunk.CodeWindowLines = v }, func(c Config) int { return c.RAG.Chunk.CodeWindowLines }),
	ki("rag.chunk.code_overlap_lines", "overlap between code chunks", 10,
		func(c *Config, v int) { c.RAG.Chunk.CodeOverlapLines = v }, func(c Config) int { return c.RAG.Chunk.CodeOverlapLines }),
	ki("rag.chunk.go_max_decl_lines", "keep a Go declaration whole up to this many lines", 200,
		func(c *Config, v int) { c.RAG.Chunk.GoMaxDeclLines = v }, func(c Config) int { return c.RAG.Chunk.GoMaxDeclLines }),
	ki("rag.chunk.go_split_window_lines", "window once a Go declaration is split", 120,
		func(c *Config, v int) { c.RAG.Chunk.GoSplitWindowLines = v }, func(c Config) int { return c.RAG.Chunk.GoSplitWindowLines }),
	ki("rag.chunk.go_split_overlap_lines", "overlap for a split Go declaration", 20,
		func(c *Config, v int) { c.RAG.Chunk.GoSplitOverlapLines = v }, func(c Config) int { return c.RAG.Chunk.GoSplitOverlapLines }),
	ki("rag.search.vector_top", "vector candidates before fusion", 30,
		func(c *Config, v int) { c.RAG.Search.VectorTop = v }, func(c Config) int { return c.RAG.Search.VectorTop }),
	ki("rag.search.text_top", "text candidates before fusion", 30,
		func(c *Config, v int) { c.RAG.Search.TextTop = v }, func(c Config) int { return c.RAG.Search.TextTop }),
	ki("rag.search.rrf_k", "reciprocal-rank fusion constant", 60,
		func(c *Config, v int) { c.RAG.Search.RRFK = v }, func(c Config) int { return c.RAG.Search.RRFK }),
	ki("rag.search.rerank_top", "candidates sent to the reranker", 20,
		func(c *Config, v int) { c.RAG.Search.RerankTop = v }, func(c Config) int { return c.RAG.Search.RerankTop }),
	ki("rag.search.k", "chunks returned to the model", 6,
		func(c *Config, v int) { c.RAG.Search.K = v }, func(c Config) int { return c.RAG.Search.K }),
	kf("rag.search.rerank_threshold", "drop reranked chunks at or below this score", 0.1,
		func(c *Config, v float64) { c.RAG.Search.RerankThreshold = v }, func(c Config) float64 { return c.RAG.Search.RerankThreshold }).chk(CheckRatioClosed),
	ki("rag.index.max_file_kb", "skip files larger than this", 512,
		func(c *Config, v int) { c.RAG.Index.MaxFileKB = v }, func(c Config) int { return c.RAG.Index.MaxFileKB }),
	ki("rag.index.confirm_over_minutes", "ask before an index expected to take longer", 5,
		func(c *Config, v int) { c.RAG.Index.ConfirmOverMinutes = v }, func(c Config) int { return c.RAG.Index.ConfirmOverMinutes }),

	ki("ui.display_lines", "tool-output lines kept on screen", 60,
		func(c *Config, v int) { c.UI.DisplayLines = v }, func(c Config) int { return c.UI.DisplayLines }),
	ki("ui.paste_chip_lines", "lines shown for a pasted chip", 11,
		func(c *Config, v int) { c.UI.PasteChipLines = v }, func(c Config) int { return c.UI.PasteChipLines }),
	ki("ui.paste_chip_chars", "characters shown for a pasted chip", 1000,
		func(c *Config, v int) { c.UI.PasteChipChars = v }, func(c Config) int { return c.UI.PasteChipChars }),
	ki("ui.output_keep_kb", "tool output retained for the model", 256,
		func(c *Config, v int) { c.UI.OutputKeepKB = v }, func(c Config) int { return c.UI.OutputKeepKB }),
	ks("ui.theme", "amber | glacier | nebula | jade | rose | mono", "", "amber",
		func(c *Config, v string) { c.UI.Theme = v }, func(c Config) string { return c.UI.Theme }).chk(CheckTheme).alias("ui.palette").named("theme").when(ApplyNow),
	ks("ui.appearance", "auto | dark | light", "", "auto",
		func(c *Config, v string) { c.UI.Appearance = v }, func(c Config) string { return c.UI.Appearance }).chk(CheckAppearance).allowed("auto", "dark", "light").named("appearance").when(ApplyNow),
	kb("ui.cursor_color", "color the prompt cursor", true,
		func(c *Config, v bool) { c.UI.CursorColor = v }, func(c Config) bool { return c.UI.CursorColor }).named("cursor").when(ApplyNow),
	kb("ui.banner", "draw the wordmark at startup", true,
		func(c *Config, v bool) { c.UI.Banner = v }, func(c Config) bool { return c.UI.Banner }).named("banner").when(ApplyNext),
	ks("ui.welcome", "split | frame | minimal", "", "split",
		func(c *Config, v string) { c.UI.Welcome = v }, func(c Config) string { return c.UI.Welcome }).allowed("split", "frame", "minimal").named("welcome").when(ApplyNext),
	kb("ui.code_highlight", "highlight fenced code", true,
		func(c *Config, v bool) { c.UI.CodeHighlight = v }, func(c Config) bool { return c.UI.CodeHighlight }).named("highlight").when(ApplyNow),
	kb("ui.clear_on_start", "clear the terminal when the REPL starts", true,
		func(c *Config, v bool) { c.UI.ClearOnStart = v }, func(c Config) bool { return c.UI.ClearOnStart }),
	kb("ui.strip_emoji", "drop emoji from model text", true,
		func(c *Config, v bool) { c.UI.StripEmoji = v }, func(c Config) bool { return c.UI.StripEmoji }).named("emoji").when(ApplyNow),
	km("ui.probe_deadline_ms", "welcome gather budget", 400,
		func(c *Config, v int) { c.UI.ProbeDeadlineMS = v }, func(c Config) int { return c.UI.ProbeDeadlineMS }),
	km("ui.probe_timeout_ms", "one endpoint probe", 300,
		func(c *Config, v int) { c.UI.ProbeTimeoutMS = v }, func(c Config) int { return c.UI.ProbeTimeoutMS }),
	km("ui.esc_timeout_ms", "gap allowed between bytes of one escape sequence", 30,
		func(c *Config, v int) { c.UI.EscTimeoutMS = v }, func(c Config) int { return c.UI.EscTimeoutMS }).when(ApplyNow),
	km("ui.git_query_ms", "git status during welcome and in the footer", 200,
		func(c *Config, v int) { c.UI.GitQueryMS = v }, func(c Config) int { return c.UI.GitQueryMS }),
	km("ui.memory_query_ms", "facts and recent sessions on the welcome screen", 200,
		func(c *Config, v int) { c.UI.MemoryQueryMS = v }, func(c Config) int { return c.UI.MemoryQueryMS }),
	km("ui.startup_cap_ms", "longest wait before the prompt appears", 500,
		func(c *Config, v int) { c.UI.StartupCapMS = v }, func(c Config) int { return c.UI.StartupCapMS }),
	ki("ui.welcome_cache_seconds", "refresh a cached welcome in the background after this", 60,
		func(c *Config, v int) { c.UI.WelcomeCacheSeconds = v }, func(c Config) int { return c.UI.WelcomeCacheSeconds }),
	ki("ui.model_rate_calls", "recent calls used for tokens per second", 20,
		func(c *Config, v int) { c.UI.ModelRateCalls = v }, func(c Config) int { return c.UI.ModelRateCalls }),
	km("ui.model_slow_ms", "welcome marks a model slow at this latency", 1000,
		func(c *Config, v int) { c.UI.ModelSlowMS = v }, func(c Config) int { return c.UI.ModelSlowMS }),
	ki("ui.ctx_warn_pct", "footer meter turns warn at this context percent", 60,
		func(c *Config, v int) { c.UI.CtxWarnPct = v }, func(c Config) int { return c.UI.CtxWarnPct }).chk(CheckWarnPct).named("ctx").when(ApplyNow),
	kb("ui.trace", "print per-call traces", false,
		func(c *Config, v bool) { c.UI.Trace = v }, func(c Config) bool { return c.UI.Trace }).named("trace").when(ApplyNow),
	kb("ui.stats", "print the end-of-turn stats line", true,
		func(c *Config, v bool) { c.UI.Stats = v }, func(c Config) bool { return c.UI.Stats }).named("stats").when(ApplyNow),
	kb("ui.badge", "print the intent badge", true,
		func(c *Config, v bool) { c.UI.Badge = v }, func(c Config) bool { return c.UI.Badge }).named("badge").when(ApplyNow),
	ki("ui.ctx_hot_pct", "footer meter turns hot at this context percent", 85,
		func(c *Config, v int) { c.UI.CtxHotPct = v }, func(c Config) int { return c.UI.CtxHotPct }),
	ki("ui.ctx_cells", "cells in the footer context meter", 8,
		func(c *Config, v int) { c.UI.CtxCells = v }, func(c Config) int { return c.UI.CtxCells }),
	ki("ui.ctx_narrow_cols", "below this width the meter is text only", 90,
		func(c *Config, v int) { c.UI.CtxNarrowCols = v }, func(c Config) int { return c.UI.CtxNarrowCols }),
	ki("ui.layout.tiny_height", "below this height the welcome is a short strip", 22,
		func(c *Config, v int) { c.UI.Layout.TinyHeight = v }, func(c Config) int { return c.UI.Layout.TinyHeight }),
	ki("ui.layout.recent_height", "at or above this, show the recent panel", 30,
		func(c *Config, v int) { c.UI.Layout.RecentHeight = v }, func(c Config) int { return c.UI.Layout.RecentHeight }),
	ki("ui.layout.tiny_width", "below this width, skip the wordmark", 64,
		func(c *Config, v int) { c.UI.Layout.TinyWidth = v }, func(c Config) int { return c.UI.Layout.TinyWidth }),
	ki("ui.layout.narrow_width", "below this, stack the welcome instead of splitting it", 110,
		func(c *Config, v int) { c.UI.Layout.NarrowWidth = v }, func(c Config) int { return c.UI.Layout.NarrowWidth }),
	ki("ui.layout.frame_width", "frame layout needs at least this width", 120,
		func(c *Config, v int) { c.UI.Layout.FrameWidth = v }, func(c Config) int { return c.UI.Layout.FrameWidth }),

	ki("tools.shell.default_timeout_sec", "run_shell timeout when the model omits one", 120,
		func(c *Config, v int) { c.Tools.Shell.DefaultTimeoutSec = v }, func(c Config) int { return c.Tools.Shell.DefaultTimeoutSec }).named("timeout").when(ApplyNext),
	ki("tools.shell.max_timeout_sec", "longest run_shell timeout", 600,
		func(c *Config, v int) { c.Tools.Shell.MaxTimeoutSec = v }, func(c Config) int { return c.Tools.Shell.MaxTimeoutSec }),
	km("tools.shell.wait_delay_ms", "how long a cancelled shell may take to exit", 2000,
		func(c *Config, v int) { c.Tools.Shell.WaitDelayMS = v }, func(c Config) int { return c.Tools.Shell.WaitDelayMS }),
	kb("tools.web.enabled", "offer the web_fetch tool", true,
		func(c *Config, v bool) { c.Tools.Web.Enabled = v }, func(c Config) bool { return c.Tools.Web.Enabled }).named("web").when(ApplyNext),
	knn("tools.web.results", "web result cap; 0 means no cap", 0,
		func(c *Config, v int) { c.Tools.Web.Results = v }, func(c Config) int { return c.Tools.Web.Results }).when(ApplyNext),

	km("store.query_timeout_ms", "database query limit", 2000,
		func(c *Config, v int) { c.Store.QueryTimeoutMS = v }, func(c Config) int { return c.Store.QueryTimeoutMS }),
	km("store.migrate_timeout_ms", "schema migration limit", 15000,
		func(c *Config, v int) { c.Store.MigrateTimeoutMS = v }, func(c Config) int { return c.Store.MigrateTimeoutMS }),
	km("stats.insert_timeout_ms", "turn stat insert limit", 2000,
		func(c *Config, v int) { c.Stats.InsertTimeoutMS = v }, func(c Config) int { return c.Stats.InsertTimeoutMS }),

	ks("language.mode", "auto | en | fr", "", "auto",
		func(c *Config, v string) { c.Language.Mode = v }, func(c Config) string { return c.Language.Mode }).allowed("auto", "en", "fr").named("language").when(ApplyNext),
	kli("language.languages", "reply languages the router may pick", []string{"en", "fr"},
		func(c *Config, v []string) { c.Language.Languages = copyList(v) }, func(c Config) []string { return c.Language.Languages }).named("languages").when(ApplyNext),
	ks("language.default", "language used when detection is unsure", "", "en",
		func(c *Config, v string) { c.Language.Default = v }, func(c Config) string { return c.Language.Default }),

	kli("safety.extra_read_deny", "extra path prefixes to refuse", []string{},
		func(c *Config, v []string) { c.Safety.ExtraReadDeny = copyList(v) }, func(c Config) []string { return c.Safety.ExtraReadDeny }),
	kli("safety.extra_command_deny", "extra command substrings to refuse", []string{},
		func(c *Config, v []string) { c.Safety.ExtraCommandDeny = copyList(v) }, func(c Config) []string { return c.Safety.ExtraCommandDeny }),

	kli("router.action_verbs", "verbs that suggest an act or build turn", nil,
		func(c *Config, v []string) { c.Router.ActionVerbs = copyList(v) }, func(c Config) []string { return c.Router.ActionVerbs }),
	kli("router.targets", "nouns that suggest a project target", nil,
		func(c *Config, v []string) { c.Router.Targets = copyList(v) }, func(c Config) []string { return c.Router.Targets }),
}

type trieNode struct {
	key  *Key
	kids map[string]*trieNode
}

var rootTrie *trieNode

func init() {
	for i := range keyTable {
		switch keyTable[i].Path {
		case "router.action_verbs":
			keyTable[i].List = defaultActionVerbs()
		case "router.targets":
			keyTable[i].List = defaultTargets()
		}
	}
	rootTrie = &trieNode{kids: map[string]*trieNode{}}
	seen := map[string]bool{}
	for i := range keyTable {
		k := &keyTable[i]
		if seen[k.Path] {
			panic("duplicate config key " + k.Path)
		}
		seen[k.Path] = true
		insertTrie(k.Path, k)
		for _, a := range k.Aliases {
			if seen[a] {
				panic("duplicate config alias " + a)
			}
			seen[a] = true
			insertTrie(a, k)
		}
	}
}

func insertTrie(path string, k *Key) {
	cur := rootTrie
	parts := strings.Split(path, ".")
	for i, p := range parts {
		if cur.kids == nil {
			cur.kids = map[string]*trieNode{}
		}
		n := cur.kids[p]
		if n == nil {
			n = &trieNode{}
			cur.kids[p] = n
		}
		last := i == len(parts)-1
		if last {
			if n.key != nil || len(n.kids) > 0 {
				panic("config key conflict " + path)
			}
			n.key = k
		} else if n.key != nil {
			panic("config key conflict " + path)
		}
		cur = n
	}
}

// ApplyKeyDefaults writes every table default onto c.
func ApplyKeyDefaults(c *Config) {
	if c == nil {
		return
	}
	for i := range keyTable {
		k := &keyTable[i]
		switch k.Kind {
		case KindString, KindEnum:
			k.SetStr(c, k.Str)
		case KindBool:
			k.SetBool(c, k.Bool)
		case KindInt, KindDurationMS:
			k.SetInt(c, k.Int)
		case KindFloat:
			k.SetFloat(c, k.Float)
		case KindList:
			k.SetList(c, copyList(k.List))
		}
	}
}

type walkState struct {
	c         *Config
	apply     bool
	src       map[string]string
	source    string
	mentioned map[string]bool
	sawModels bool
}

func checkOverlay(n *yaml.Node, full string) error {
	return walkOverlay(n, rootTrie, "", full, nil)
}

func applyOverlayNode(c *Config, n *yaml.Node, full, source string, src map[string]string) error {
	return walkOverlay(n, rootTrie, "", full, &walkState{c: c, apply: true, src: src, source: source})
}

func walkOverlay(n *yaml.Node, t *trieNode, rel, full string, st *walkState) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return walkOverlay(n.Content[0], t, rel, full, st)
	}
	if n.Kind == yaml.AliasNode {
		return walkOverlay(n.Alias, t, rel, full, st)
	}
	if n.Kind != yaml.MappingNode {
		if full == "" {
			return fmt.Errorf("root must be a mapping (line %d)", n.Line)
		}
		return fmt.Errorf("%s must be a mapping (line %d)", full, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		name := kn.Value
		childFull := name
		if full != "" {
			childFull = full + "." + name
		}
		if name == "models" && rel == "" {
			if st != nil {
				st.sawModels = true
				if st.mentioned == nil {
					st.mentioned = map[string]bool{}
				}
			}
			var mentioned map[string]bool
			var target *Config
			if st != nil && st.apply {
				target = st.c
				mentioned = st.mentioned
			}
			if err := applyModelMap(target, vn, childFull, mentioned); err != nil {
				return err
			}
			continue
		}
		if t == nil || t.kids[name] == nil {
			return fmt.Errorf("unknown key %q at line %d", childFull, kn.Line)
		}
		child := t.kids[name]
		if child.key != nil && len(child.kids) == 0 {
			if err := applyValue(st, child.key, vn, childFull); err != nil {
				return err
			}
			continue
		}
		childRel := name
		if rel != "" {
			childRel = rel + "." + name
		}
		if err := walkOverlay(vn, child, childRel, childFull, st); err != nil {
			return err
		}
	}
	return nil
}

func isNull(n *yaml.Node) bool {
	if n == nil {
		return true
	}
	return n.Tag == "!!null" || n.Kind == 0
}

func applyValue(st *walkState, k *Key, n *yaml.Node, full string) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyValue(st, k, n.Alias, full)
	}
	switch k.Kind {
	case KindString, KindEnum:
		if n.Kind != yaml.ScalarNode {
			return fmt.Errorf("config: %s must be a string (line %d)", full, n.Line)
		}
		if st != nil && st.apply && st.c != nil {
			k.SetStr(st.c, n.Value)
			noteSrc(st, k.Path)
		}
	case KindBool:
		b, err := parseBool(n, full)
		if err != nil {
			return err
		}
		if st != nil && st.apply && st.c != nil {
			k.SetBool(st.c, b)
			noteSrc(st, k.Path)
		}
	case KindInt, KindDurationMS:
		v, err := parseInt(n, full)
		if err != nil {
			return err
		}
		if st != nil && st.apply && st.c != nil {
			k.SetInt(st.c, v)
			noteSrc(st, k.Path)
		}
	case KindFloat:
		v, err := parseFloat(n, full)
		if err != nil {
			return err
		}
		if st != nil && st.apply && st.c != nil {
			k.SetFloat(st.c, v)
			noteSrc(st, k.Path)
		}
	case KindList:
		items, err := parseList(n, full)
		if err != nil {
			return err
		}
		if st != nil && st.apply && st.c != nil {
			k.SetList(st.c, items)
			noteSrc(st, k.Path)
		}
	}
	return nil
}

func noteSrc(st *walkState, path string) {
	if st != nil && st.src != nil && st.source != "" {
		st.src[path] = st.source
	}
}

func parseBool(n *yaml.Node, full string) (bool, error) {
	if n.Kind != yaml.ScalarNode {
		return false, fmt.Errorf("config: %s must be a bool (line %d)", full, n.Line)
	}
	switch strings.ToLower(strings.TrimSpace(n.Value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("config: %s must be a bool (line %d)", full, n.Line)
	}
}

func parseInt(n *yaml.Node, full string) (int, error) {
	if n.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("config: %s must be an integer (line %d)", full, n.Line)
	}
	v, err := strconv.Atoi(strings.TrimSpace(n.Value))
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer (line %d)", full, n.Line)
	}
	return v, nil
}

func parseFloat(n *yaml.Node, full string) (float64, error) {
	if n.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("config: %s must be a number (line %d)", full, n.Line)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(n.Value), 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a number (line %d)", full, n.Line)
	}
	return v, nil
}

func parseList(n *yaml.Node, full string) ([]string, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("config: %s must be a list (line %d)", full, n.Line)
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		if c.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("config: %s must be a list of strings (line %d)", full, c.Line)
		}
		out = append(out, c.Value)
	}
	return out, nil
}

func applyKeyEnv(c *Config, src map[string]string) error {
	for i := range keyTable {
		k := &keyTable[i]
		if k.Env == "" {
			continue
		}
		v, ok := os.LookupEnv(k.Env)
		if !ok || v == "" {
			continue
		}
		if err := k.setFromString(c, v); err != nil {
			return fmt.Errorf("config: %s: %w", k.Env, err)
		}
		if src != nil {
			src[k.Path] = "env"
		}
	}
	return nil
}

func (k Key) setFromString(c *Config, raw string) error {
	switch k.Kind {
	case KindString, KindEnum:
		k.SetStr(c, raw)
	case KindBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("config: %s must be a bool", k.Path)
		}
		k.SetBool(c, b)
	case KindInt, KindDurationMS:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("config: %s must be an integer", k.Path)
		}
		k.SetInt(c, n)
	case KindFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("config: %s must be a number", k.Path)
		}
		k.SetFloat(c, f)
	case KindList:
		parts := strings.Split(raw, ",")
		items := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				items = append(items, p)
			}
		}
		k.SetList(c, items)
	}
	return nil
}

func validateConfig(c Config) error {
	for i := range keyTable {
		if err := keyTable[i].validate(c); err != nil {
			return err
		}
	}
	if c.Context.ElideTarget >= c.Context.ElideTrigger {
		return fmt.Errorf("config: context.elide_target (%g) must be < context.elide_trigger (%g)", c.Context.ElideTarget, c.Context.ElideTrigger)
	}
	if c.Tools.Shell.DefaultTimeoutSec > c.Tools.Shell.MaxTimeoutSec {
		return fmt.Errorf("config: tools.shell.default_timeout_sec (%d) must be <= tools.shell.max_timeout_sec (%d)", c.Tools.Shell.DefaultTimeoutSec, c.Tools.Shell.MaxTimeoutSec)
	}
	if c.UI.CtxHotPct <= c.UI.CtxWarnPct || c.UI.CtxHotPct > 100 {
		return fmt.Errorf("config: ui.ctx_hot_pct must be greater than ui.ctx_warn_pct and at most 100, got %d", c.UI.CtxHotPct)
	}
	if err := validateModels(c); err != nil {
		return err
	}
	return nil
}

func (k Key) validate(c Config) error {
	switch k.Check {
	case CheckNone:
		return nil
	case CheckPositive:
		n := k.GetInt(c)
		if n <= 0 {
			return fmt.Errorf("config: %s must be a positive integer, got %d", k.Path, n)
		}
	case CheckRatio:
		v := k.GetFloat(c)
		if v <= 0 || v >= 1 {
			return fmt.Errorf("config: %s must be in (0,1), got %g", k.Path, v)
		}
	case CheckRatioClosed:
		v := k.GetFloat(c)
		if v <= 0 || v > 1 {
			return fmt.Errorf("config: %s must be in (0,1], got %g", k.Path, v)
		}
	case CheckURL:
		return requireURL(k.Path, k.GetStr(c))
	case CheckURLOptional:
		s := strings.TrimSpace(k.GetStr(c))
		if s == "" {
			return nil
		}
		return requireURL(k.Path, s)
	case CheckDBURL:
		s := strings.TrimSpace(k.GetStr(c))
		if s == "" {
			return nil
		}
		return requireDBURL(s)
	case CheckTheme:
		s := strings.TrimSpace(k.GetStr(c))
		if s == "" {
			return nil
		}
		if _, ok := ResolveTheme(s); !ok {
			return fmt.Errorf("config: ui.theme must be amber, glacier, nebula, jade, rose or mono, got %q", k.GetStr(c))
		}
	case CheckAppearance:
		switch strings.ToLower(strings.TrimSpace(k.GetStr(c))) {
		case "", "auto", "dark", "light":
			return nil
		default:
			return fmt.Errorf("config: ui.appearance must be auto, dark or light, got %q", k.GetStr(c))
		}
	case CheckWarnPct:
		n := k.GetInt(c)
		if n <= 0 || n >= 100 {
			return fmt.Errorf("config: %s must be in (0,100), got %d", k.Path, n)
		}
	case CheckTemp:
		v := k.GetFloat(c)
		if v <= 0 || v > 2 {
			return fmt.Errorf("config: %s must be in (0,2], got %g", k.Path, v)
		}
	case CheckTopP:
		v := k.GetFloat(c)
		if v == 0 {
			return nil
		}
		if v <= 0 || v > 1 {
			return fmt.Errorf("config: %s must be 0 or in (0,1], got %g", k.Path, v)
		}
	case CheckNonNeg:
		n := k.GetInt(c)
		if n < 0 {
			return fmt.Errorf("config: %s must be >= 0, got %d", k.Path, n)
		}
	}
	if len(k.Allowed) > 0 && k.GetStr != nil {
		v := strings.ToLower(strings.TrimSpace(k.GetStr(c)))
		if v == "" {
			return nil
		}
		ok := false
		for _, a := range k.Allowed {
			if v == a {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("config: %s must be %s, got %q", k.Path, strings.Join(k.Allowed, ", "), k.GetStr(c))
		}
	}
	return nil
}

func (k Key) format(c Config) string {
	return k.present(c, false)
}

func (k Key) present(c Config, mask bool) string {
	switch k.Kind {
	case KindString, KindEnum:
		raw := k.GetStr(c)
		if mask && k.Secret {
			raw = MaskURL(raw)
		}
		return yamlScalar(raw)
	case KindBool:
		return strconv.FormatBool(k.GetBool(c))
	case KindInt, KindDurationMS:
		return strconv.Itoa(k.GetInt(c))
	case KindFloat:
		return strconv.FormatFloat(k.GetFloat(c), 'g', -1, 64)
	case KindList:
		return flowList(k.GetList(c))
	default:
		return `""`
	}
}

func yamlScalar(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimSpace(string(b))
}

func flowList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(s))
	}
	b.WriteByte(']')
	return b.String()
}

const initHeader = `# Konsolai config. CLI flags beat KONSOLAI_* env vars, then the
# selected profile, then defaults, then these built-in values.
# Do not put API keys or passwords in this file. database.url is a
# connection string; /config show masks the password.
`

func renderInitYAML() string {
	return renderDoc(Builtin(), "home", false)
}

func renderCurrentYAML(c Config) string {
	name := c.Name
	if name == "" {
		name = builtinProfile
	}
	return renderDoc(c, name, false)
}

func renderShow(c Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "profile: %s\n", c.Name)
	writeKeys(&b, c, true, 0) // secrets masked
	return b.String()
}

func renderDoc(c Config, profile string, mask bool) string {
	var b strings.Builder
	b.WriteString(initHeader)
	fmt.Fprintf(&b, "default_profile: %s   # used when --profile and KONSOLAI_PROFILE are empty\n\n", yamlScalar(profile))
	b.WriteString("defaults:\n")
	writeModels(&b, c, 1)
	writeKeys(&b, c, mask, 1)
	b.WriteString("\nprofiles:\n")
	fmt.Fprintf(&b, "  %s: {}\n", yamlScalar(profile))
	return b.String()
}

func writeKeys(b *strings.Builder, c Config, mask bool, indentOff int) {
	var open []string
	for i := range keyTable {
		k := &keyTable[i]
		parts := strings.Split(k.Path, ".")
		for i := 0; i < len(parts)-1; i++ {
			prefix := strings.Join(parts[:i+1], ".")
			if i < len(open) && open[i] == prefix {
				continue
			}
			open = open[:i]
			fmt.Fprintf(b, "%s%s:\n", strings.Repeat("  ", indentOff+i), parts[i])
			open = append(open, prefix)
		}
		val := k.present(c, mask)
		fmt.Fprintf(b, "%s%s: %s   # %s\n", strings.Repeat("  ", indentOff+len(parts)-1), parts[len(parts)-1], val, k.Doc)
	}
}

// DiffText lists keys whose effective value differs from the built-in default.
// The source is file, env, or flag.
func DiffText(path, profile string) (string, error) {
	f, err := Load(path)
	if err != nil {
		return "", err
	}
	base := Builtin()
	normalizeLanguage(&base)
	normalizeUI(&base)
	fileCfg, err := f.resolve(profile, false)
	if err != nil {
		return "", err
	}
	full, err := f.resolve(profile, true)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	writeModelDiff(&b, base, fileCfg, full)
	for i := range keyTable {
		k := &keyTable[i]
		dv := k.present(base, true)
		fv := k.present(fileCfg, true)
		ev := k.present(full, true)
		if ev == dv {
			continue
		}
		source := "file"
		if ev != fv {
			source = "env"
		}
		fmt.Fprintf(&b, "%s  %s -> %s  (%s)\n", k.Path, dv, ev, source)
	}
	if b.Len() == 0 {
		return "no differences\n", nil
	}
	return b.String(), nil
}

// EnvOverrideTable is the README table of file overrides.
// Rows with a key path come from the Env column, in table order.
// The following rows are overrides that are not a key column.
func EnvOverrideTable() string {
	var b strings.Builder
	b.WriteString("| Variable | Sets |\n| --- | --- |\n")
	for i := range keyTable {
		k := &keyTable[i]
		if k.Env == "" {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | `%s` |\n", k.Env, k.Path)
	}
	extras := [][2]string{
		{"KONSOLAI_ENDPOINT", "main model endpoint"},
		{"KONSOLAI_MODEL", "main model id"},
		{"KONSOLAI_FAST_ENDPOINT", "fast model endpoint"},
		{"KONSOLAI_FAST_MODEL", "fast model id"},
		{"KONSOLAI_EMBED_ENDPOINT", "`roles.embed`"},
		{"KONSOLAI_RERANK_ENDPOINT", "`roles.rerank`"},
		{"KONSOLAI_PROFILE", "profile name, when `--profile` is empty"},
		{"KONSOLAI_CONFIG", "config file, when `--config` is empty"},
	}
	for _, row := range extras {
		fmt.Fprintf(&b, "| `%s` | %s |\n", row[0], row[1])
	}
	return b.String()
}
