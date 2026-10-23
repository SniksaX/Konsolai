package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// File is a parsed config document. Profiles lists the profile names.
type File struct {
	DefaultProfile string
	Profiles       map[string]struct{}
	Notice         string
	defaults       *yaml.Node
	profileNodes   map[string]*yaml.Node
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
	if len(bytes.TrimSpace(b)) == 0 {
		return File{}, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return File{}, fmt.Errorf("config: decode %s: %w", path, err)
	}
	f, err := fileFromDoc(&doc)
	if err != nil {
		return File{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return f, nil
}

func fileFromDoc(doc *yaml.Node) (File, error) {
	n := doc
	if n != nil && n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return File{}, nil
		}
		n = n.Content[0]
	}
	if isNull(n) {
		return File{}, nil
	}
	if n.Kind != yaml.MappingNode {
		return File{}, fmt.Errorf("root must be a mapping (line %d)", n.Line)
	}
	f := File{Profiles: map[string]struct{}{}, profileNodes: map[string]*yaml.Node{}}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		switch k.Value {
		case "default_profile":
			if v.Kind != yaml.ScalarNode || isNull(v) {
				return File{}, fmt.Errorf("default_profile must be a string (line %d)", v.Line)
			}
			f.DefaultProfile = v.Value
		case "defaults":
			migrateLegacyShape(v)
			if err := checkOverlay(v, "defaults"); err != nil {
				return File{}, err
			}
			f.defaults = v
		case "profiles":
			if isNull(v) {
				continue
			}
			if v.Kind != yaml.MappingNode {
				return File{}, fmt.Errorf("profiles must be a mapping (line %d)", v.Line)
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				pk := v.Content[j]
				pv := v.Content[j+1]
				full := "profiles." + pk.Value
				migrateLegacyShape(pv)
				if err := checkOverlay(pv, full); err != nil {
					return File{}, err
				}
				f.Profiles[pk.Value] = struct{}{}
				if f.profileNodes == nil {
					f.profileNodes = map[string]*yaml.Node{}
				}
				f.profileNodes[pk.Value] = pv
			}
		default:
			return File{}, fmt.Errorf("unknown key %q at line %d", k.Value, k.Line)
		}
	}
	return f, nil
}

func (f File) Resolve(name string) (Config, error) {
	return f.resolve(name, true)
}

func (f File) resolve(name string, useEnv bool) (Config, error) {
	name = selectProfile(f, name)
	if _, ok := f.profileNodes[name]; !ok && name != builtinProfile {
		return Config{}, fmt.Errorf("config: unknown profile %q", name)
	}
	c := Builtin()
	c.Name = name
	st := &walkState{c: &c, apply: true, mentioned: map[string]bool{}}
	if f.defaults != nil {
		st.source = "file"
		if err := walkOverlay(f.defaults, rootTrie, "", "defaults", st); err != nil {
			return Config{}, err
		}
	}
	if n := f.profileNodes[name]; n != nil {
		st.source = "file"
		if err := walkOverlay(n, rootTrie, "", "profiles."+name, st); err != nil {
			return Config{}, err
		}
	}
	pruneBuiltinModel(&c, st.mentioned, st.sawModels)
	if useEnv {
		if err := applyKeyEnv(&c, nil); err != nil {
			return Config{}, err
		}
		if err := applyModelEnv(&c, nil); err != nil {
			return Config{}, err
		}
	}
	normalizeLanguage(&c)
	normalizeUI(&c)
	if err := validateConfig(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func selectProfile(f File, name string) string {
	if name == "" {
		name = os.Getenv("KONSOLAI_PROFILE")
	}
	if name == "" {
		name = f.DefaultProfile
	}
	if name == "" {
		name = builtinProfile
	}
	return name
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
	if c.Agent.PlanThink == "" {
		c.Agent.PlanThink = "on_reject"
	}
}

func normalizeUI(c *Config) {
	theme := strings.ToLower(strings.TrimSpace(c.UI.Theme))
	appear := strings.ToLower(strings.TrimSpace(c.UI.Appearance))
	if isOldAppearance(theme) {
		if appear == "" {
			appear = theme
		}
		theme = ""
	}
	if name, ok := ResolveTheme(theme); ok {
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
	return validateConfig(c)
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
	out, err := fileYAML(old)
	if err != nil {
		return "", err
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
	out := File{
		DefaultProfile: old.DefaultProfile,
		Profiles:       map[string]struct{}{},
		profileNodes:   map[string]*yaml.Node{},
	}
	for name, p := range old.Profiles {
		out.Profiles[name] = struct{}{}
		out.profileNodes[name] = overlayFromOld(p)
	}
	return out, nil
}

func overlayFromOld(p oldProfile) *yaml.Node {
	root := mapNode()
	if p.Endpoint != "" || p.Model != "" || p.LLMIdleTimeoutSeconds != 0 {
		m := mapNode()
		if p.Endpoint != "" {
			put(m, "endpoint", scalarNode(p.Endpoint))
		}
		if p.Model != "" {
			put(m, "model", scalarNode(p.Model))
		}
		if p.LLMIdleTimeoutSeconds != 0 {
			put(m, "idle_timeout_seconds", intNode(p.LLMIdleTimeoutSeconds))
		}
		put(root, "llm", m)
	}
	if p.FastEndpoint != "" || p.FastModel != "" {
		m := mapNode()
		if p.FastEndpoint != "" {
			put(m, "endpoint", scalarNode(p.FastEndpoint))
		}
		if p.FastModel != "" {
			put(m, "model", scalarNode(p.FastModel))
		}
		put(root, "fast", m)
	}
	if p.DatabaseURL != "" {
		m := mapNode()
		put(m, "url", scalarNode(p.DatabaseURL))
		put(root, "database", m)
	}
	if p.SessionIdleMinutes != 0 || p.RetentionDays != 0 {
		m := mapNode()
		if p.SessionIdleMinutes != 0 {
			put(m, "session_idle_minutes", intNode(p.SessionIdleMinutes))
		}
		if p.RetentionDays != 0 {
			put(m, "retention_days", intNode(p.RetentionDays))
		}
		put(root, "memory", m)
	}
	if p.BuildThinking != "" {
		m := mapNode()
		put(m, "build_thinking", scalarNode(p.BuildThinking))
		put(root, "agent", m)
	}
	if len(p.Languages) > 0 || p.DefaultLanguage != "" {
		m := mapNode()
		if len(p.Languages) > 0 {
			put(m, "languages", listNode(p.Languages))
		}
		if p.DefaultLanguage != "" {
			put(m, "default", scalarNode(p.DefaultLanguage))
		}
		put(root, "language", m)
	}
	if p.EmbedEndpoint != "" || p.RerankEndpoint != "" || p.MemoryRAG != nil {
		m := mapNode()
		if p.EmbedEndpoint != "" {
			put(m, "embed_endpoint", scalarNode(p.EmbedEndpoint))
		}
		if p.RerankEndpoint != "" {
			put(m, "rerank_endpoint", scalarNode(p.RerankEndpoint))
		}
		if p.MemoryRAG != nil {
			put(m, "enabled", boolNode(*p.MemoryRAG))
		}
		put(root, "rag", m)
	}
	return root
}

func fileYAML(f File) ([]byte, error) {
	root := mapNode()
	if f.DefaultProfile != "" {
		put(root, "default_profile", scalarNode(f.DefaultProfile))
	}
	if len(f.profileNodes) > 0 {
		pm := mapNode()
		names := make([]string, 0, len(f.profileNodes))
		for name := range f.profileNodes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			node := f.profileNodes[name]
			if node == nil {
				node = mapNode()
			}
			put(pm, name, node)
		}
		put(root, "profiles", pm)
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("config: marshal yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("config: marshal yaml: %w", err)
	}
	return buf.Bytes(), nil
}

func mapNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func scalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func intNode(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(n)}
}

func boolNode(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

func listNode(items []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, s := range items {
		n.Content = append(n.Content, scalarNode(s))
	}
	return n
}

func put(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, scalarNode(key), val)
}
