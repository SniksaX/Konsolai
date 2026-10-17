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
default_profile: home

defaults:
  llm:
    endpoint: %s
    model: %q
    idle_timeout_seconds: %d
    max_tokens:
      answer: %d
      lookup: %d
      act: %d
      build: %d
  fast:
    enabled: %t
    endpoint: %s
    model: %q
    context_guard_ratio: %g
  database:
    url: %q
  memory:
    session_idle_minutes: %d
    retention_days: %d
    compact_threshold: %g
  context:
    elide_trigger: %g
    elide_target: %g
  agent:
    max_steps:
      lookup: %d
      act: %d
      build: %d
    explore_budget:
      lookup: %d
      lookup_analysis: %d
      act: %d
      build: %d
      build_docs: %d
      build_analysis: %d
    build_thinking: %s
  rag:
    enabled: %t
    embed_endpoint: %s
    rerank_endpoint: %s
    embed_batch: %d
    chunk:
      turn_tokens: %d
      web_tokens: %d
      web_overlap_tokens: %d
      code_window_lines: %d
      code_overlap_lines: %d
      go_max_decl_lines: %d
      go_split_window_lines: %d
      go_split_overlap_lines: %d
    search:
      vector_top: %d
      text_top: %d
      rrf_k: %d
      rerank_top: %d
      k: %d
      rerank_threshold: %g
    index:
      max_file_kb: %d
      confirm_over_minutes: %d
  ui:
    display_lines: %d
    paste_chip_lines: %d
    paste_chip_chars: %d
    output_keep_kb: %d
    theme: %s   # amber | glacier | nebula | jade | rose | mono
    appearance: %s   # auto | dark | light
    cursor_color: %t
    banner: %t
    welcome: %s   # split | frame | minimal
    code_highlight: %t
    clear_on_start: %t
    strip_emoji: %t
  language:
    languages: %s
    default: %s
  safety:
    extra_read_deny: []
    extra_command_deny: []
  router:
    action_verbs: %s
    targets: %s

profiles:
  home: {}
  work:
    llm:
      endpoint: %s
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
		b.Agent.BuildThinking,
		b.RAG.Enabled, b.RAG.EmbedEndpoint, b.RAG.RerankEndpoint, b.RAG.EmbedBatch,
		b.RAG.Chunk.TurnTokens, b.RAG.Chunk.WebTokens, b.RAG.Chunk.WebOverlapTokens,
		b.RAG.Chunk.CodeWindowLines, b.RAG.Chunk.CodeOverlapLines,
		b.RAG.Chunk.GoMaxDeclLines, b.RAG.Chunk.GoSplitWindowLines, b.RAG.Chunk.GoSplitOverlapLines,
		b.RAG.Search.VectorTop, b.RAG.Search.TextTop, b.RAG.Search.RRFK,
		b.RAG.Search.RerankTop, b.RAG.Search.K, b.RAG.Search.RerankThreshold,
		b.RAG.Index.MaxFileKB, b.RAG.Index.ConfirmOverMinutes,
		b.UI.DisplayLines, b.UI.PasteChipLines, b.UI.PasteChipChars, b.UI.OutputKeepKB,
		b.UI.Theme, b.UI.Appearance, b.UI.CursorColor, b.UI.Banner, b.UI.Welcome, b.UI.CodeHighlight, b.UI.ClearOnStart, b.UI.StripEmoji,
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
