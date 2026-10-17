package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

type File struct {
	DefaultProfile string             `yaml:"default_profile" json:"default_profile"`
	Defaults       Overlay            `yaml:"defaults" json:"defaults"`
	Profiles       map[string]Overlay `yaml:"profiles" json:"profiles"`
	Notice         string             `yaml:"-" json:"-"`
}

type Overlay struct {
	LLM      *LLMOverlay      `yaml:"llm,omitempty" json:"llm,omitempty"`
	Fast     *FastOverlay     `yaml:"fast,omitempty" json:"fast,omitempty"`
	Database *DatabaseOverlay `yaml:"database,omitempty" json:"database,omitempty"`
	Memory   *MemoryOverlay   `yaml:"memory,omitempty" json:"memory,omitempty"`
	Context  *ContextOverlay  `yaml:"context,omitempty" json:"context,omitempty"`
	Agent    *AgentOverlay    `yaml:"agent,omitempty" json:"agent,omitempty"`
	RAG      *RAGOverlay      `yaml:"rag,omitempty" json:"rag,omitempty"`
	UI       *UIOverlay       `yaml:"ui,omitempty" json:"ui,omitempty"`
	Language *LanguageOverlay `yaml:"language,omitempty" json:"language,omitempty"`
	Safety   *SafetyOverlay   `yaml:"safety,omitempty" json:"safety,omitempty"`
	Router   *RouterOverlay   `yaml:"router,omitempty" json:"router,omitempty"`
}

type LLMOverlay struct {
	Endpoint           *string           `yaml:"endpoint" json:"endpoint"`
	Model              *string           `yaml:"model" json:"model"`
	IdleTimeoutSeconds *int              `yaml:"idle_timeout_seconds" json:"idle_timeout_seconds"`
	MaxTokens          *MaxTokensOverlay `yaml:"max_tokens" json:"max_tokens"`
}

type MaxTokensOverlay struct {
	Answer *int `yaml:"answer" json:"answer"`
	Lookup *int `yaml:"lookup" json:"lookup"`
	Act    *int `yaml:"act" json:"act"`
	Build  *int `yaml:"build" json:"build"`
}

type FastOverlay struct {
	Enabled           *bool    `yaml:"enabled" json:"enabled"`
	Endpoint          *string  `yaml:"endpoint" json:"endpoint"`
	Model             *string  `yaml:"model" json:"model"`
	ContextGuardRatio *float64 `yaml:"context_guard_ratio" json:"context_guard_ratio"`
}

type DatabaseOverlay struct {
	URL *string `yaml:"url" json:"url"`
}

type MemoryOverlay struct {
	SessionIdleMinutes *int     `yaml:"session_idle_minutes" json:"session_idle_minutes"`
	RetentionDays      *int     `yaml:"retention_days" json:"retention_days"`
	CompactThreshold   *float64 `yaml:"compact_threshold" json:"compact_threshold"`
}

type ContextOverlay struct {
	ElideTrigger *float64 `yaml:"elide_trigger" json:"elide_trigger"`
	ElideTarget  *float64 `yaml:"elide_target" json:"elide_target"`
}

type AgentOverlay struct {
	MaxSteps         *StepsOverlay         `yaml:"max_steps" json:"max_steps"`
	ExploreBudget    *ExploreBudgetOverlay `yaml:"explore_budget" json:"explore_budget"`
	BuildThinking    *string               `yaml:"build_thinking" json:"build_thinking"`
	IdenticalRejects *int                  `yaml:"identical_rejects" json:"identical_rejects"`
}

type StepsOverlay struct {
	Lookup *int `yaml:"lookup" json:"lookup"`
	Act    *int `yaml:"act" json:"act"`
	Build  *int `yaml:"build" json:"build"`
}

type ExploreBudgetOverlay struct {
	Lookup         *int `yaml:"lookup" json:"lookup"`
	LookupAnalysis *int `yaml:"lookup_analysis" json:"lookup_analysis"`
	Act            *int `yaml:"act" json:"act"`
	Build          *int `yaml:"build" json:"build"`
	BuildDocs      *int `yaml:"build_docs" json:"build_docs"`
	BuildAnalysis  *int `yaml:"build_analysis" json:"build_analysis"`
}

type RAGOverlay struct {
	Enabled        *bool             `yaml:"enabled" json:"enabled"`
	EmbedEndpoint  *string           `yaml:"embed_endpoint" json:"embed_endpoint"`
	RerankEndpoint *string           `yaml:"rerank_endpoint" json:"rerank_endpoint"`
	EmbedBatch     *int              `yaml:"embed_batch" json:"embed_batch"`
	Chunk          *RAGChunkOverlay  `yaml:"chunk" json:"chunk"`
	Search         *RAGSearchOverlay `yaml:"search" json:"search"`
	Index          *RAGIndexOverlay  `yaml:"index" json:"index"`
}

type RAGChunkOverlay struct {
	TurnTokens          *int `yaml:"turn_tokens" json:"turn_tokens"`
	WebTokens           *int `yaml:"web_tokens" json:"web_tokens"`
	WebOverlapTokens    *int `yaml:"web_overlap_tokens" json:"web_overlap_tokens"`
	CodeWindowLines     *int `yaml:"code_window_lines" json:"code_window_lines"`
	CodeOverlapLines    *int `yaml:"code_overlap_lines" json:"code_overlap_lines"`
	GoMaxDeclLines      *int `yaml:"go_max_decl_lines" json:"go_max_decl_lines"`
	GoSplitWindowLines  *int `yaml:"go_split_window_lines" json:"go_split_window_lines"`
	GoSplitOverlapLines *int `yaml:"go_split_overlap_lines" json:"go_split_overlap_lines"`
}

type RAGSearchOverlay struct {
	VectorTop       *int     `yaml:"vector_top" json:"vector_top"`
	TextTop         *int     `yaml:"text_top" json:"text_top"`
	RRFK            *int     `yaml:"rrf_k" json:"rrf_k"`
	RerankTop       *int     `yaml:"rerank_top" json:"rerank_top"`
	K               *int     `yaml:"k" json:"k"`
	RerankThreshold *float64 `yaml:"rerank_threshold" json:"rerank_threshold"`
}

type RAGIndexOverlay struct {
	MaxFileKB          *int `yaml:"max_file_kb" json:"max_file_kb"`
	ConfirmOverMinutes *int `yaml:"confirm_over_minutes" json:"confirm_over_minutes"`
}

type UIOverlay struct {
	DisplayLines        *int             `yaml:"display_lines" json:"display_lines"`
	PasteChipLines      *int             `yaml:"paste_chip_lines" json:"paste_chip_lines"`
	PasteChipChars      *int             `yaml:"paste_chip_chars" json:"paste_chip_chars"`
	OutputKeepKB        *int             `yaml:"output_keep_kb" json:"output_keep_kb"`
	Palette             *string          `yaml:"palette" json:"palette"`
	Banner              *bool            `yaml:"banner" json:"banner"`
	Welcome             *string          `yaml:"welcome" json:"welcome"`
	Theme               *string          `yaml:"theme" json:"theme"`
	Appearance          *string          `yaml:"appearance" json:"appearance"`
	CursorColor         *bool            `yaml:"cursor_color" json:"cursor_color"`
	CodeHighlight       *bool            `yaml:"code_highlight" json:"code_highlight"`
	ClearOnStart        *bool            `yaml:"clear_on_start" json:"clear_on_start"`
	StripEmoji          *bool            `yaml:"strip_emoji" json:"strip_emoji"`
	ProbeDeadlineMS     *int             `yaml:"probe_deadline_ms" json:"probe_deadline_ms"`
	ProbeTimeoutMS      *int             `yaml:"probe_timeout_ms" json:"probe_timeout_ms"`
	GitQueryMS          *int             `yaml:"git_query_ms" json:"git_query_ms"`
	MemoryQueryMS       *int             `yaml:"memory_query_ms" json:"memory_query_ms"`
	StartupCapMS        *int             `yaml:"startup_cap_ms" json:"startup_cap_ms"`
	WelcomeCacheSeconds *int             `yaml:"welcome_cache_seconds" json:"welcome_cache_seconds"`
	ModelRateCalls      *int             `yaml:"model_rate_calls" json:"model_rate_calls"`
	CtxWarnPct          *int             `yaml:"ctx_warn_pct" json:"ctx_warn_pct"`
	CtxHotPct           *int             `yaml:"ctx_hot_pct" json:"ctx_hot_pct"`
	CtxCells            *int             `yaml:"ctx_cells" json:"ctx_cells"`
	CtxNarrowCols       *int             `yaml:"ctx_narrow_cols" json:"ctx_narrow_cols"`
	Layout              *UILayoutOverlay `yaml:"layout" json:"layout"`
}

type UILayoutOverlay struct {
	TinyHeight   *int `yaml:"tiny_height" json:"tiny_height"`
	RecentHeight *int `yaml:"recent_height" json:"recent_height"`
	TinyWidth    *int `yaml:"tiny_width" json:"tiny_width"`
	NarrowWidth  *int `yaml:"narrow_width" json:"narrow_width"`
	FrameWidth   *int `yaml:"frame_width" json:"frame_width"`
}

type LanguageOverlay struct {
	Languages *[]string `yaml:"languages" json:"languages"`
	Default   *string   `yaml:"default" json:"default"`
}

type SafetyOverlay struct {
	ExtraReadDeny    *[]string `yaml:"extra_read_deny" json:"extra_read_deny"`
	ExtraCommandDeny *[]string `yaml:"extra_command_deny" json:"extra_command_deny"`
}

type RouterOverlay struct {
	ActionVerbs *[]string `yaml:"action_verbs" json:"action_verbs"`
	Targets     *[]string `yaml:"targets" json:"targets"`
}

func Load(path string) (File, error) {
	f, notice, err := LoadNotice(path)
	f.Notice = notice
	return f, err
}

func LoadNotice(path string) (File, string, error) {
	if path == "" {
		path = DefaultPath()
	}
	notice := ""
	if path == DefaultPath() {
		n, err := migrateJSON(path)
		if err != nil {
			return File{}, "", err
		}
		notice = n
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, notice, nil
		}
		return File{}, notice, fmt.Errorf("config: read %s: %w", path, err)
	}
	migrated, err := migrateUIKeys(path, b)
	if err != nil {
		return File{}, notice, err
	}
	if migrated {
		b, err = os.ReadFile(path)
		if err != nil {
			return File{}, notice, fmt.Errorf("config: read %s: %w", path, err)
		}
		if notice != "" {
			notice += "; "
		}
		notice += "migrated ui.theme / ui.palette → ui.appearance / ui.theme"
	}
	f, err := Parse(b, path)
	if err != nil {
		return File{}, notice, err
	}
	return f, notice, nil
}

func Parse(b []byte, path string) (File, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return File{}, fmt.Errorf("config: decode %s: %w", path, err)
	}
	if err := checkKnown(&doc, rootSchema, ""); err != nil {
		return File{}, fmt.Errorf("config: %s: %w", path, err)
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("config: decode %s: %w", path, err)
	}
	return f, nil
}

func (f File) Resolve(name string) (Config, error) {
	if name == "" {
		name = os.Getenv("KONSOLAI_PROFILE")
	}
	if name == "" {
		name = f.DefaultProfile
	}
	if name == "" {
		name = builtinProfile
	}
	ov, ok := f.Profiles[name]
	if !ok {
		if name != builtinProfile {
			return Config{}, fmt.Errorf("config: unknown profile %q", name)
		}
		ov = Overlay{}
	}
	c := Builtin()
	c.Name = name
	applyOverlay(&c, f.Defaults)
	applyOverlay(&c, ov)
	applyEnv(&c)
	normalizeLanguage(&c)
	normalizeUI(&c)
	c.syncFlat()
	if err := Validate(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func applyEnv(c *Config) {
	if v := os.Getenv("KONSOLAI_DATABASE_URL"); v != "" {
		c.Database.URL = v
	}
	if v := os.Getenv("KONSOLAI_ENDPOINT"); v != "" {
		c.LLM.Endpoint = v
	}
	if v := os.Getenv("KONSOLAI_MODEL"); v != "" {
		c.LLM.Model = v
	}
	if v := os.Getenv("KONSOLAI_FAST_ENDPOINT"); v != "" {
		c.Fast.Endpoint = v
	}
	if v := os.Getenv("KONSOLAI_FAST_MODEL"); v != "" {
		c.Fast.Model = v
	}
	if v := os.Getenv("KONSOLAI_BUILD_THINKING"); v != "" {
		c.Agent.BuildThinking = v
	}
	if v := os.Getenv("KONSOLAI_EMBED_ENDPOINT"); v != "" {
		c.RAG.EmbedEndpoint = v
	}
	if v := os.Getenv("KONSOLAI_RERANK_ENDPOINT"); v != "" {
		c.RAG.RerankEndpoint = v
	}
}

func normalizeLanguage(c *Config) {
	if len(c.Language.Languages) == 0 {
		c.Language.Languages = []string{"en", "fr"}
	} else {
		out := make([]string, 0, len(c.Language.Languages))
		for _, l := range c.Language.Languages {
			l = strings.ToLower(strings.TrimSpace(l))
			if l != "" {
				out = append(out, l)
			}
		}
		if len(out) == 0 {
			out = []string{"en", "fr"}
		}
		c.Language.Languages = out
	}
	c.Language.Default = strings.ToLower(strings.TrimSpace(c.Language.Default))
	if c.Language.Default == "" {
		c.Language.Default = "en"
	}
	if !langAllowed(c.Language.Default, c.Language.Languages) {
		c.Language.Default = c.Language.Languages[0]
	}
	if c.Agent.BuildThinking == "" {
		c.Agent.BuildThinking = "plan_step"
	}
}

func normalizeUI(c *Config) {
	theme := strings.ToLower(strings.TrimSpace(c.UI.Theme))
	appear := strings.ToLower(strings.TrimSpace(c.UI.Appearance))
	palette := strings.ToLower(strings.TrimSpace(c.UI.Palette))
	if isOldAppearance(theme) {
		if appear == "" {
			appear = theme
		}
		theme = ""
	}
	if name, ok := ResolveTheme(theme); ok {
		theme = name
	} else if name, ok := ResolveTheme(palette); ok {
		theme = name
	} else {
		theme = "amber"
	}
	switch appear {
	case "light", "dark":
	default:
		appear = "auto"
	}
	c.UI.Theme = theme
	c.UI.Appearance = appear
	c.UI.Palette = ""
}

func langAllowed(code string, langs []string) bool {
	for _, l := range langs {
		if l == code {
			return true
		}
	}
	return false
}

func Validate(c Config) error {
	if err := requirePositive("llm.idle_timeout_seconds", c.LLM.IdleTimeoutSeconds); err != nil {
		return err
	}
	if err := requirePositive("llm.max_tokens.answer", c.LLM.MaxTokens.Answer); err != nil {
		return err
	}
	if err := requirePositive("llm.max_tokens.lookup", c.LLM.MaxTokens.Lookup); err != nil {
		return err
	}
	if err := requirePositive("llm.max_tokens.act", c.LLM.MaxTokens.Act); err != nil {
		return err
	}
	if err := requirePositive("llm.max_tokens.build", c.LLM.MaxTokens.Build); err != nil {
		return err
	}
	if err := requireURL("llm.endpoint", c.LLM.Endpoint); err != nil {
		return err
	}
	if err := requireURL("fast.endpoint", c.Fast.Endpoint); err != nil {
		return err
	}
	if err := requireRatio("fast.context_guard_ratio", c.Fast.ContextGuardRatio); err != nil {
		return err
	}
	if c.Database.URL != "" {
		if err := requireDBURL(c.Database.URL); err != nil {
			return err
		}
	}
	if err := requirePositive("memory.session_idle_minutes", c.Memory.SessionIdleMinutes); err != nil {
		return err
	}
	if err := requirePositive("memory.retention_days", c.Memory.RetentionDays); err != nil {
		return err
	}
	if err := requireRatio("memory.compact_threshold", c.Memory.CompactThreshold); err != nil {
		return err
	}
	if err := requireRatio("context.elide_trigger", c.Context.ElideTrigger); err != nil {
		return err
	}
	if err := requireRatio("context.elide_target", c.Context.ElideTarget); err != nil {
		return err
	}
	if c.Context.ElideTarget >= c.Context.ElideTrigger {
		return fmt.Errorf("config: context.elide_target (%g) must be < context.elide_trigger (%g)", c.Context.ElideTarget, c.Context.ElideTrigger)
	}
	if err := requirePositive("agent.max_steps.lookup", c.Agent.MaxSteps.Lookup); err != nil {
		return err
	}
	if err := requirePositive("agent.max_steps.act", c.Agent.MaxSteps.Act); err != nil {
		return err
	}
	if err := requirePositive("agent.max_steps.build", c.Agent.MaxSteps.Build); err != nil {
		return err
	}
	for _, pair := range []struct {
		name string
		n    int
	}{
		{"agent.explore_budget.lookup", c.Agent.ExploreBudget.Lookup},
		{"agent.explore_budget.lookup_analysis", c.Agent.ExploreBudget.LookupAnalysis},
		{"agent.explore_budget.act", c.Agent.ExploreBudget.Act},
		{"agent.explore_budget.build", c.Agent.ExploreBudget.Build},
		{"agent.explore_budget.build_docs", c.Agent.ExploreBudget.BuildDocs},
		{"agent.explore_budget.build_analysis", c.Agent.ExploreBudget.BuildAnalysis},
		{"rag.embed_batch", c.RAG.EmbedBatch},
		{"rag.chunk.turn_tokens", c.RAG.Chunk.TurnTokens},
		{"rag.chunk.web_tokens", c.RAG.Chunk.WebTokens},
		{"rag.chunk.web_overlap_tokens", c.RAG.Chunk.WebOverlapTokens},
		{"rag.chunk.code_window_lines", c.RAG.Chunk.CodeWindowLines},
		{"rag.chunk.code_overlap_lines", c.RAG.Chunk.CodeOverlapLines},
		{"rag.chunk.go_max_decl_lines", c.RAG.Chunk.GoMaxDeclLines},
		{"rag.chunk.go_split_window_lines", c.RAG.Chunk.GoSplitWindowLines},
		{"rag.chunk.go_split_overlap_lines", c.RAG.Chunk.GoSplitOverlapLines},
		{"rag.search.vector_top", c.RAG.Search.VectorTop},
		{"rag.search.text_top", c.RAG.Search.TextTop},
		{"rag.search.rrf_k", c.RAG.Search.RRFK},
		{"rag.search.rerank_top", c.RAG.Search.RerankTop},
		{"rag.search.k", c.RAG.Search.K},
		{"rag.index.max_file_kb", c.RAG.Index.MaxFileKB},
		{"rag.index.confirm_over_minutes", c.RAG.Index.ConfirmOverMinutes},
		{"ui.display_lines", c.UI.DisplayLines},
		{"ui.paste_chip_lines", c.UI.PasteChipLines},
		{"ui.paste_chip_chars", c.UI.PasteChipChars},
		{"ui.output_keep_kb", c.UI.OutputKeepKB},
		{"agent.identical_rejects", c.Agent.IdenticalRejects},
		{"ui.probe_deadline_ms", c.UI.ProbeDeadlineMS},
		{"ui.probe_timeout_ms", c.UI.ProbeTimeoutMS},
		{"ui.git_query_ms", c.UI.GitQueryMS},
		{"ui.memory_query_ms", c.UI.MemoryQueryMS},
		{"ui.startup_cap_ms", c.UI.StartupCapMS},
		{"ui.welcome_cache_seconds", c.UI.WelcomeCacheSeconds},
		{"ui.model_rate_calls", c.UI.ModelRateCalls},
		{"ui.ctx_cells", c.UI.CtxCells},
		{"ui.ctx_narrow_cols", c.UI.CtxNarrowCols},
		{"ui.layout.tiny_height", c.UI.Layout.TinyHeight},
		{"ui.layout.recent_height", c.UI.Layout.RecentHeight},
		{"ui.layout.tiny_width", c.UI.Layout.TinyWidth},
		{"ui.layout.narrow_width", c.UI.Layout.NarrowWidth},
		{"ui.layout.frame_width", c.UI.Layout.FrameWidth},
	} {
		if err := requirePositive(pair.name, pair.n); err != nil {
			return err
		}
	}
	if err := requireURL("rag.embed_endpoint", c.RAG.EmbedEndpoint); err != nil {
		return err
	}
	if err := requireURL("rag.rerank_endpoint", c.RAG.RerankEndpoint); err != nil {
		return err
	}
	if c.UI.CtxWarnPct <= 0 || c.UI.CtxWarnPct >= 100 {
		return fmt.Errorf("config: ui.ctx_warn_pct must be in (0,100), got %d", c.UI.CtxWarnPct)
	}
	if c.UI.CtxHotPct <= c.UI.CtxWarnPct || c.UI.CtxHotPct > 100 {
		return fmt.Errorf("config: ui.ctx_hot_pct must be greater than ui.ctx_warn_pct and at most 100, got %d", c.UI.CtxHotPct)
	}
	if c.RAG.Search.RerankThreshold <= 0 || c.RAG.Search.RerankThreshold > 1 {
		return fmt.Errorf("config: rag.search.rerank_threshold must be in (0,1], got %g", c.RAG.Search.RerankThreshold)
	}
	if c.LLM.Model != "" && strings.TrimSpace(c.LLM.Model) == "" {
		return fmt.Errorf("config: llm.model is empty")
	}
	if pal := strings.ToLower(strings.TrimSpace(c.UI.Palette)); pal != "" {
		if _, ok := ResolveTheme(pal); !ok {
			return fmt.Errorf("config: ui.palette must be a known theme or alias, got %q", c.UI.Palette)
		}
	}
	if _, ok := ResolveTheme(c.UI.Theme); !ok && strings.TrimSpace(c.UI.Theme) != "" {
		return fmt.Errorf("config: ui.theme must be amber, glacier, nebula, jade, rose or mono, got %q", c.UI.Theme)
	}
	switch strings.ToLower(strings.TrimSpace(c.UI.Appearance)) {
	case "", "auto", "dark", "light":
	default:
		return fmt.Errorf("config: ui.appearance must be auto, dark or light, got %q", c.UI.Appearance)
	}
	return nil
}

func requirePositive(name string, n int) error {
	if n <= 0 {
		return fmt.Errorf("config: %s must be a positive integer, got %d", name, n)
	}
	return nil
}

func requireRatio(name string, v float64) error {
	if v <= 0 || v >= 1 {
		return fmt.Errorf("config: %s must be in (0,1), got %g", name, v)
	}
	return nil
}

func requireURL(name, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("config: %s is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("config: %s is not a valid URL: %q", name, raw)
	}
	return nil
}

func requireDBURL(raw string) error {
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return fmt.Errorf("config: database.url is not a valid URL: %q", raw)
		}
		return nil
	}
	if strings.Contains(raw, "host=") || strings.Contains(raw, "dbname=") {
		return nil
	}
	return fmt.Errorf("config: database.url is not a valid URL: %q", raw)
}

func migrateJSON(yamlPath string) (string, error) {
	if _, err := os.Stat(yamlPath); err == nil {
		return "", nil
	}
	jsonPath := filepath.Join(filepath.Dir(yamlPath), "config.json")
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("config: read %s: %w", jsonPath, err)
	}
	old, err := parseOldJSON(b, jsonPath)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(old)
	if err != nil {
		return "", fmt.Errorf("config: marshal yaml: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(yamlPath), 0700); err != nil {
		return "", fmt.Errorf("config: mkdir: %w", err)
	}
	if err := os.WriteFile(yamlPath, out, 0600); err != nil {
		return "", fmt.Errorf("config: write %s: %w", yamlPath, err)
	}
	bak := jsonPath + ".bak"
	if err := os.Rename(jsonPath, bak); err != nil {
		return "", fmt.Errorf("config: rename %s: %w", jsonPath, err)
	}
	return "migrated " + jsonPath + " to " + yamlPath + " (json saved as " + filepath.Base(bak) + ")", nil
}

type oldFile struct {
	DefaultProfile string                `json:"default_profile"`
	Profiles       map[string]oldProfile `json:"profiles"`
}

type oldProfile struct {
	DatabaseURL           string   `json:"database_url"`
	Endpoint              string   `json:"endpoint"`
	Model                 string   `json:"model"`
	FastEndpoint          string   `json:"fast_endpoint"`
	FastModel             string   `json:"fast_model"`
	SessionIdleMinutes    int      `json:"session_idle_minutes"`
	RetentionDays         int      `json:"retention_days"`
	LLMIdleTimeoutSeconds int      `json:"llm_idle_timeout_seconds"`
	BuildThinking         string   `json:"build_thinking"`
	Languages             []string `json:"languages"`
	DefaultLanguage       string   `json:"default_language"`
	EmbedEndpoint         string   `json:"embed_endpoint"`
	RerankEndpoint        string   `json:"rerank_endpoint"`
	MemoryRAG             *bool    `json:"memory_rag"`
}

func parseOldJSON(b []byte, path string) (File, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var old oldFile
	if err := dec.Decode(&old); err != nil {
		return File{}, fmt.Errorf("config: decode %s: %w", path, err)
	}
	out := File{DefaultProfile: old.DefaultProfile, Profiles: map[string]Overlay{}}
	for name, p := range old.Profiles {
		out.Profiles[name] = overlayFromOld(p)
	}
	return out, nil
}

func overlayFromOld(p oldProfile) Overlay {
	ov := Overlay{}
	if p.Endpoint != "" || p.Model != "" || p.LLMIdleTimeoutSeconds != 0 {
		ov.LLM = &LLMOverlay{}
		if p.Endpoint != "" {
			ov.LLM.Endpoint = strPtr(p.Endpoint)
		}
		if p.Model != "" {
			ov.LLM.Model = strPtr(p.Model)
		}
		if p.LLMIdleTimeoutSeconds != 0 {
			ov.LLM.IdleTimeoutSeconds = intPtr(p.LLMIdleTimeoutSeconds)
		}
	}
	if p.FastEndpoint != "" || p.FastModel != "" {
		ov.Fast = &FastOverlay{}
		if p.FastEndpoint != "" {
			ov.Fast.Endpoint = strPtr(p.FastEndpoint)
		}
		if p.FastModel != "" {
			ov.Fast.Model = strPtr(p.FastModel)
		}
	}
	if p.DatabaseURL != "" {
		ov.Database = &DatabaseOverlay{URL: strPtr(p.DatabaseURL)}
	}
	if p.SessionIdleMinutes != 0 || p.RetentionDays != 0 {
		ov.Memory = &MemoryOverlay{}
		if p.SessionIdleMinutes != 0 {
			ov.Memory.SessionIdleMinutes = intPtr(p.SessionIdleMinutes)
		}
		if p.RetentionDays != 0 {
			ov.Memory.RetentionDays = intPtr(p.RetentionDays)
		}
	}
	if p.BuildThinking != "" {
		ov.Agent = &AgentOverlay{BuildThinking: strPtr(p.BuildThinking)}
	}
	if len(p.Languages) > 0 || p.DefaultLanguage != "" {
		ov.Language = &LanguageOverlay{}
		if len(p.Languages) > 0 {
			langs := append([]string(nil), p.Languages...)
			ov.Language.Languages = &langs
		}
		if p.DefaultLanguage != "" {
			ov.Language.Default = strPtr(p.DefaultLanguage)
		}
	}
	if p.EmbedEndpoint != "" || p.RerankEndpoint != "" || p.MemoryRAG != nil {
		ov.RAG = &RAGOverlay{}
		if p.EmbedEndpoint != "" {
			ov.RAG.EmbedEndpoint = strPtr(p.EmbedEndpoint)
		}
		if p.RerankEndpoint != "" {
			ov.RAG.RerankEndpoint = strPtr(p.RerankEndpoint)
		}
		if p.MemoryRAG != nil {
			ov.RAG.Enabled = p.MemoryRAG
		}
	}
	return ov
}

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }
func floatPtr(f float64) *float64 {
	return &f
}
func boolPtr(b bool) *bool { return &b }
