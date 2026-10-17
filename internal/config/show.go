package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

type showView struct {
	LLM      LLM      `yaml:"llm"`
	Fast     Fast     `yaml:"fast"`
	Database Database `yaml:"database"`
	Memory   Memory   `yaml:"memory"`
	Context  Context  `yaml:"context"`
	Agent    Agent    `yaml:"agent"`
	RAG      RAG      `yaml:"rag"`
	UI       UI       `yaml:"ui"`
	Language Language `yaml:"language"`
	Safety   Safety   `yaml:"safety"`
	Router   Router   `yaml:"router"`
}

func (c Config) ShowYAML() (string, error) {
	v := showView{
		LLM: c.LLM, Fast: c.Fast, Database: c.Database, Memory: c.Memory,
		Context: c.Context, Agent: c.Agent, RAG: c.RAG, UI: c.UI,
		Language: c.Language, Safety: c.Safety, Router: c.Router,
	}
	v.Database.URL = MaskURL(v.Database.URL)
	b, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("config: show: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "profile: %s\n", c.Name)
	out.Write(b)
	return out.String(), nil
}

func WriteInit(path string, force bool) error {
	if path == "" {
		path = DefaultPath()
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config: %s already exists (use --force to overwrite)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	if err := os.WriteFile(path, []byte(InitYAML()), 0600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

func InitYAML() string {
	b := Builtin()
	verbs := yamlList(b.Router.ActionVerbs)
	targets := yamlList(b.Router.Targets)
	langs := yamlList(b.Language.Languages)
	return fmt.Sprintf(`# Konsolai config. CLI flags beat KONSOLAI_* env vars, then the
# selected profile, then defaults, then these built-in values.
# Do not put API keys or passwords in this file. database.url is a
# connection string; konsolai config show masks the password.
default_profile: home   # used when --profile and KONSOLAI_PROFILE are empty

defaults:
  llm:
    endpoint: %s   # OpenAI-compatible API base
    model: %q   # empty uses the server default
    idle_timeout_seconds: %d   # stream idle limit
    max_tokens:
      answer: %d   # answer intent token cap
      lookup: %d   # lookup intent token cap
      act: %d   # act intent token cap
      build: %d   # build intent token cap
  fast:
    enabled: %t   # route short turns to the fast model
    endpoint: %s   # fast model API base
    model: %q   # empty uses the server default
    context_guard_ratio: %g   # skip fast when the prompt fills past this fraction of its window
  database:
    url: %q   # Postgres URL; empty disables memory
  memory:
    session_idle_minutes: %d   # resume a session newer than this
    retention_days: %d   # prune facts older than this
    compact_threshold: %g   # compact history past this fraction of the window
  context:
    elide_trigger: %g   # start eliding tool output above this fraction
    elide_target: %g   # elide down toward this fraction; must stay below the trigger
  agent:
    max_steps:
      lookup: %d   # lookup turn step cap
      act: %d   # act turn step cap
      build: %d   # build turn step cap
    explore_budget:
      lookup: %d   # read-only steps before the lookup nudge
      lookup_analysis: %d   # lookup analysis read budget
      act: %d   # act read budget before the nudge
      build: %d   # build read budget before the plan nudge
      build_docs: %d   # docs-task read budget
      build_analysis: %d   # build analysis read budget
    build_thinking: %s   # plan_step | until_plan
    identical_rejects: %d   # stop after this many identical rejected tool calls
  rag:
    enabled: %t   # index and search project memory
    embed_endpoint: %s   # embedding service
    rerank_endpoint: %s   # rerank service
    embed_batch: %d   # texts per embed request
    chunk:
      turn_tokens: %d   # tokens per stored turn chunk
      web_tokens: %d   # tokens per fetched-page chunk
      web_overlap_tokens: %d   # overlap between web chunks
      code_window_lines: %d   # lines per code chunk
      code_overlap_lines: %d   # overlap between code chunks
      go_max_decl_lines: %d   # keep a Go declaration whole up to this many lines
      go_split_window_lines: %d   # window once a Go declaration is split
      go_split_overlap_lines: %d   # overlap for a split Go declaration
    search:
      vector_top: %d   # vector candidates before fusion
      text_top: %d   # text candidates before fusion
      rrf_k: %d   # reciprocal-rank fusion constant
      rerank_top: %d   # candidates sent to the reranker
      k: %d   # chunks returned to the model
      rerank_threshold: %g   # drop reranked chunks at or below this score
    index:
      max_file_kb: %d   # skip files larger than this
      confirm_over_minutes: %d   # ask before an index expected to take longer
  ui:
    display_lines: %d   # tool-output lines kept on screen
    paste_chip_lines: %d   # lines shown for a pasted chip
    paste_chip_chars: %d   # characters shown for a pasted chip
    output_keep_kb: %d   # tool output retained for the model
    theme: %s   # amber | glacier | nebula | jade | rose | mono
    appearance: %s   # auto | dark | light
    cursor_color: %t   # color the prompt cursor
    banner: %t   # draw the wordmark at startup
    welcome: %s   # split | frame | minimal
    code_highlight: %t   # highlight fenced code
    clear_on_start: %t   # clear the terminal when the REPL starts
    strip_emoji: %t   # drop emoji from model text
    probe_deadline_ms: %d   # welcome gather budget
    probe_timeout_ms: %d   # one endpoint probe
    git_query_ms: %d   # git status during welcome and in the footer
    memory_query_ms: %d   # facts and recent sessions on the welcome screen
    startup_cap_ms: %d   # longest wait before the prompt appears
    welcome_cache_seconds: %d   # refresh a cached welcome in the background after this
    model_rate_calls: %d   # recent calls used for tokens per second
    ctx_warn_pct: %d   # footer meter turns warn at this context percent
    ctx_hot_pct: %d   # footer meter turns hot at this context percent
    ctx_cells: %d   # cells in the footer context meter
    ctx_narrow_cols: %d   # below this width the meter is text only
    layout:
      tiny_height: %d   # below this height the welcome is a short strip
      recent_height: %d   # at or above this, show the recent panel
      tiny_width: %d   # below this width, skip the wordmark
      narrow_width: %d   # below this, stack the welcome instead of splitting it
      frame_width: %d   # frame layout needs at least this width
  language:
    languages: %s   # reply languages the router may pick
    default: %s   # language used when detection is unsure
  safety:
    extra_read_deny: []   # extra path prefixes to refuse
    extra_command_deny: []   # extra command substrings to refuse
  router:
    action_verbs: %s   # verbs that suggest an act or build turn
    targets: %s   # nouns that suggest a project target

profiles:
  home: {}
  work:
    llm:
      endpoint: %s   # example override; home inherits defaults
`,
		b.LLM.Endpoint, b.LLM.Model, b.LLM.IdleTimeoutSeconds,
		b.LLM.MaxTokens.Answer, b.LLM.MaxTokens.Lookup, b.LLM.MaxTokens.Act, b.LLM.MaxTokens.Build,
		b.Fast.Enabled, b.Fast.Endpoint, b.Fast.Model, b.Fast.ContextGuardRatio,
		b.Database.URL,
		b.Memory.SessionIdleMinutes, b.Memory.RetentionDays, b.Memory.CompactThreshold,
		b.Context.ElideTrigger, b.Context.ElideTarget,
		b.Agent.MaxSteps.Lookup, b.Agent.MaxSteps.Act, b.Agent.MaxSteps.Build,
		b.Agent.ExploreBudget.Lookup, b.Agent.ExploreBudget.LookupAnalysis,
		b.Agent.ExploreBudget.Act, b.Agent.ExploreBudget.Build,
		b.Agent.ExploreBudget.BuildDocs, b.Agent.ExploreBudget.BuildAnalysis,
		b.Agent.BuildThinking, b.Agent.IdenticalRejects,
		b.RAG.Enabled, b.RAG.EmbedEndpoint, b.RAG.RerankEndpoint, b.RAG.EmbedBatch,
		b.RAG.Chunk.TurnTokens, b.RAG.Chunk.WebTokens, b.RAG.Chunk.WebOverlapTokens,
		b.RAG.Chunk.CodeWindowLines, b.RAG.Chunk.CodeOverlapLines,
		b.RAG.Chunk.GoMaxDeclLines, b.RAG.Chunk.GoSplitWindowLines, b.RAG.Chunk.GoSplitOverlapLines,
		b.RAG.Search.VectorTop, b.RAG.Search.TextTop, b.RAG.Search.RRFK,
		b.RAG.Search.RerankTop, b.RAG.Search.K, b.RAG.Search.RerankThreshold,
		b.RAG.Index.MaxFileKB, b.RAG.Index.ConfirmOverMinutes,
		b.UI.DisplayLines, b.UI.PasteChipLines, b.UI.PasteChipChars, b.UI.OutputKeepKB,
		b.UI.Theme, b.UI.Appearance, b.UI.CursorColor, b.UI.Banner, b.UI.Welcome,
		b.UI.CodeHighlight, b.UI.ClearOnStart, b.UI.StripEmoji,
		b.UI.ProbeDeadlineMS, b.UI.ProbeTimeoutMS, b.UI.GitQueryMS, b.UI.MemoryQueryMS,
		b.UI.StartupCapMS, b.UI.WelcomeCacheSeconds, b.UI.ModelRateCalls,
		b.UI.CtxWarnPct, b.UI.CtxHotPct, b.UI.CtxCells, b.UI.CtxNarrowCols,
		b.UI.Layout.TinyHeight, b.UI.Layout.RecentHeight, b.UI.Layout.TinyWidth,
		b.UI.Layout.NarrowWidth, b.UI.Layout.FrameWidth,
		langs, b.Language.Default,
		verbs, targets,
		b.LLM.Endpoint,
	)
}

func yamlList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}
