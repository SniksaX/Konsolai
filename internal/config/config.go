package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

type File struct {
	DefaultProfile string             `json:"default_profile"`
	Profiles       map[string]Profile `json:"profiles"`
}

type Profile struct {
	Name                  string   `json:"-"`
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

func DefaultPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "konsolai", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "konsolai", "config.json")
	}
	return filepath.Join(home, ".config", "konsolai", "config.json")
}

func Load(path string) (File, error) {
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("config: decode %s: %w", path, err)
	}
	return f, nil
}

func (f File) Resolve(name string) (Profile, error) {
	if name == "" {
		name = os.Getenv("KONSOLAI_PROFILE")
	}
	if name == "" {
		name = f.DefaultProfile
	}
	if name == "" {
		name = builtinProfile
	}
	p, ok := f.Profiles[name]
	if !ok {
		if name != builtinProfile {
			return Profile{}, fmt.Errorf("config: unknown profile %q", name)
		}
		p = Profile{}
	}
	p.Name = name
	if p.Endpoint == "" {
		p.Endpoint = defaultEndpoint
	}
	if p.FastEndpoint == "" {
		p.FastEndpoint = defaultFastEndpoint
	}
	if p.SessionIdleMinutes == 0 {
		p.SessionIdleMinutes = defaultIdleMinutes
	}
	if p.RetentionDays == 0 {
		p.RetentionDays = defaultRetention
	}
	if p.LLMIdleTimeoutSeconds == 0 {
		p.LLMIdleTimeoutSeconds = defaultLLMIdle
	}
	if p.EmbedEndpoint == "" {
		p.EmbedEndpoint = defaultEmbedEndpoint
	}
	if p.RerankEndpoint == "" {
		p.RerankEndpoint = defaultRerankEndpoint
	}
	if p.MemoryRAG == nil {
		on := true
		p.MemoryRAG = &on
	}
	if v := os.Getenv("KONSOLAI_DATABASE_URL"); v != "" {
		p.DatabaseURL = v
	}
	if v := os.Getenv("KONSOLAI_ENDPOINT"); v != "" {
		p.Endpoint = v
	}
	if v := os.Getenv("KONSOLAI_MODEL"); v != "" {
		p.Model = v
	}
	if v := os.Getenv("KONSOLAI_FAST_ENDPOINT"); v != "" {
		p.FastEndpoint = v
	}
	if v := os.Getenv("KONSOLAI_FAST_MODEL"); v != "" {
		p.FastModel = v
	}
	if v := os.Getenv("KONSOLAI_BUILD_THINKING"); v != "" {
		p.BuildThinking = v
	}
	if v := os.Getenv("KONSOLAI_EMBED_ENDPOINT"); v != "" {
		p.EmbedEndpoint = v
	}
	if v := os.Getenv("KONSOLAI_RERANK_ENDPOINT"); v != "" {
		p.RerankEndpoint = v
	}
	if p.BuildThinking == "" {
		p.BuildThinking = "plan_step"
	}
	if len(p.Languages) == 0 {
		p.Languages = []string{"en", "fr"}
	} else {
		out := make([]string, 0, len(p.Languages))
		for _, l := range p.Languages {
			l = strings.ToLower(strings.TrimSpace(l))
			if l != "" {
				out = append(out, l)
			}
		}
		if len(out) == 0 {
			out = []string{"en", "fr"}
		}
		p.Languages = out
	}
	p.DefaultLanguage = strings.ToLower(strings.TrimSpace(p.DefaultLanguage))
	if p.DefaultLanguage == "" {
		p.DefaultLanguage = "en"
	}
	if !langAllowed(p.DefaultLanguage, p.Languages) {
		p.DefaultLanguage = p.Languages[0]
	}
	return p, nil
}

func (p Profile) RAGEnabled() bool {
	if p.MemoryRAG == nil {
		return true
	}
	return *p.MemoryRAG
}

func langAllowed(code string, langs []string) bool {
	for _, l := range langs {
		if l == code {
			return true
		}
	}
	return false
}

// Location returns host/dbname for display. It never includes the URL or secrets.
func (p Profile) Location() string {
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
