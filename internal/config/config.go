package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	urlUserinfo = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^:/?#\s]+:[^@/\s]+@`)
	userKV      = regexp.MustCompile(`(?i)\buser=\S+`)
	passKV      = regexp.MustCompile(`(?i)\bpassword=\S+`)
)

const (
	defaultEndpoint       = "http://fatboy:11436/v1"
	defaultFastEndpoint   = "http://fatboy:11435/v1"
	defaultEmbedEndpoint  = "http://fatboy:9502"
	defaultRerankEndpoint = "http://fatboy:9501"
	defaultIdleMinutes    = 30
	defaultRetention      = 90
	defaultLLMIdle        = 120
	builtinProfile        = "default"
)

// Profile is the resolved config passed down through the process.
// Nested sections are the source of truth; flat fields stay populated
// so existing callers keep compiling.
type Profile = Config

type Config struct {
	Name string `yaml:"-" json:"-"`

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

	DatabaseURL           string   `yaml:"-" json:"-"`
	Endpoint              string   `yaml:"-" json:"-"`
	Model                 string   `yaml:"-" json:"-"`
	FastEndpoint          string   `yaml:"-" json:"-"`
	FastModel             string   `yaml:"-" json:"-"`
	SessionIdleMinutes    int      `yaml:"-" json:"-"`
	RetentionDays         int      `yaml:"-" json:"-"`
	LLMIdleTimeoutSeconds int      `yaml:"-" json:"-"`
	BuildThinking         string   `yaml:"-" json:"-"`
	Languages             []string `yaml:"-" json:"-"`
	DefaultLanguage       string   `yaml:"-" json:"-"`
	EmbedEndpoint         string   `yaml:"-" json:"-"`
	RerankEndpoint        string   `yaml:"-" json:"-"`
	MemoryRAG             *bool    `yaml:"-" json:"-"`
}

type LLM struct {
	Endpoint           string    `yaml:"endpoint"`
	Model              string    `yaml:"model"`
	IdleTimeoutSeconds int       `yaml:"idle_timeout_seconds"`
	MaxTokens          MaxTokens `yaml:"max_tokens"`
}

type MaxTokens struct {
	Answer int `yaml:"answer"`
	Lookup int `yaml:"lookup"`
	Act    int `yaml:"act"`
	Build  int `yaml:"build"`
}

type Fast struct {
	Enabled           bool    `yaml:"enabled"`
	Endpoint          string  `yaml:"endpoint"`
	Model             string  `yaml:"model"`
	ContextGuardRatio float64 `yaml:"context_guard_ratio"`
}

type Database struct {
	URL string `yaml:"url"`
}

type Memory struct {
	SessionIdleMinutes int     `yaml:"session_idle_minutes"`
	RetentionDays      int     `yaml:"retention_days"`
	CompactThreshold   float64 `yaml:"compact_threshold"`
}

type Context struct {
	ElideTrigger float64 `yaml:"elide_trigger"`
	ElideTarget  float64 `yaml:"elide_target"`
}

type Agent struct {
	MaxSteps      Steps         `yaml:"max_steps"`
	ExploreBudget ExploreBudget `yaml:"explore_budget"`
	BuildThinking string        `yaml:"build_thinking"`
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
	Enabled        bool      `yaml:"enabled"`
	EmbedEndpoint  string    `yaml:"embed_endpoint"`
	RerankEndpoint string    `yaml:"rerank_endpoint"`
	EmbedBatch     int       `yaml:"embed_batch"`
	Chunk          RAGChunk  `yaml:"chunk"`
	Search         RAGSearch `yaml:"search"`
	Index          RAGIndex  `yaml:"index"`
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
	DisplayLines   int `yaml:"display_lines"`
	PasteChipLines int `yaml:"paste_chip_lines"`
	PasteChipChars int `yaml:"paste_chip_chars"`
	OutputKeepKB   int `yaml:"output_keep_kb"`
}

type Language struct {
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

func Builtin() Config {
	on := true
	c := Config{
		Name: builtinProfile,
		LLM: LLM{
			Endpoint:           defaultEndpoint,
			IdleTimeoutSeconds: defaultLLMIdle,
			MaxTokens:          MaxTokens{Answer: 4096, Lookup: 4096, Act: 4096, Build: 12288},
		},
		Fast: Fast{
			Enabled:           true,
			Endpoint:          defaultFastEndpoint,
			ContextGuardRatio: 0.60,
		},
		Memory: Memory{
			SessionIdleMinutes: defaultIdleMinutes,
			RetentionDays:      defaultRetention,
			CompactThreshold:   0.7,
		},
		Context: Context{ElideTrigger: 0.60, ElideTarget: 0.40},
		Agent: Agent{
			MaxSteps: Steps{Lookup: 10, Act: 15, Build: 60},
			ExploreBudget: ExploreBudget{
				Lookup: 6, LookupAnalysis: 10, Act: 6,
				Build: 8, BuildDocs: 16, BuildAnalysis: 12,
			},
			BuildThinking: "plan_step",
		},
		RAG: RAG{
			Enabled:        true,
			EmbedEndpoint:  defaultEmbedEndpoint,
			RerankEndpoint: defaultRerankEndpoint,
			EmbedBatch:     32,
			Chunk: RAGChunk{
				TurnTokens: 1200, WebTokens: 600, WebOverlapTokens: 80,
				CodeWindowLines: 60, CodeOverlapLines: 10,
				GoMaxDeclLines: 200, GoSplitWindowLines: 120, GoSplitOverlapLines: 20,
			},
			Search: RAGSearch{
				VectorTop: 30, TextTop: 30, RRFK: 60,
				RerankTop: 20, K: 6, RerankThreshold: 0.1,
			},
			Index: RAGIndex{MaxFileKB: 512, ConfirmOverMinutes: 5},
		},
		UI: UI{
			DisplayLines: 60, PasteChipLines: 11, PasteChipChars: 1000, OutputKeepKB: 256,
		},
		Language: Language{Languages: []string{"en", "fr"}, Default: "en"},
		Safety:   Safety{ExtraReadDeny: []string{}, ExtraCommandDeny: []string{}},
		Router: Router{
			ActionVerbs: defaultActionVerbs(),
			Targets:     defaultTargets(),
		},
		MemoryRAG: &on,
	}
	c.syncFlat()
	return c
}

func defaultActionVerbs() []string {
	return []string{
		"create", "write", "make", "add", "generate", "fix", "update", "edit",
		"change", "refactor", "rename", "delete", "remove", "install", "run",
		"build", "deploy", "commit", "move", "copy",
		"crée", "créer", "écris", "écrire", "ajoute", "ajouter", "génère", "générer",
		"corrige", "corriger", "modifie", "modifier", "supprime", "supprimer",
		"installe", "installer", "lance", "lancer", "déplace", "renomme",
	}
}

func defaultTargets() []string {
	return []string{
		"file", "folder", "directory", "project", "repo", "branch", "readme",
		"test", "function", "endpoint", "fichier", "dossier", "projet", "branche",
	}
}

func (c *Config) syncFlat() {
	if c == nil {
		return
	}
	c.DatabaseURL = c.Database.URL
	c.Endpoint = c.LLM.Endpoint
	c.Model = c.LLM.Model
	c.FastEndpoint = c.Fast.Endpoint
	c.FastModel = c.Fast.Model
	c.SessionIdleMinutes = c.Memory.SessionIdleMinutes
	c.RetentionDays = c.Memory.RetentionDays
	c.LLMIdleTimeoutSeconds = c.LLM.IdleTimeoutSeconds
	c.BuildThinking = c.Agent.BuildThinking
	c.Languages = append([]string(nil), c.Language.Languages...)
	c.DefaultLanguage = c.Language.Default
	c.EmbedEndpoint = c.RAG.EmbedEndpoint
	c.RerankEndpoint = c.RAG.RerankEndpoint
	on := c.RAG.Enabled
	c.MemoryRAG = &on
}

func (c Config) FastOn() bool {
	if c.FastEndpoint == "" && c.Fast.Endpoint == "" {
		return false
	}
	if !c.Fast.Enabled && c.Fast.Endpoint == "" && c.Fast.ContextGuardRatio == 0 {
		return true
	}
	return c.Fast.Enabled
}

func (c Config) RAGEnabled() bool {
	if c.MemoryRAG != nil {
		return *c.MemoryRAG
	}
	return c.RAG.Enabled
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
	host, db := hostDB(p.DatabaseURL)
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
