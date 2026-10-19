package config

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const builtinModel = "default"

func validModelName(name string) bool {
	if name == "" || strings.Contains(name, ".") || strings.ContainsAny(name, " \t\n:/") {
		return false
	}
	return true
}

func (c *Config) editMain(fn func(*Model)) {
	if c.Models == nil {
		c.Models = map[string]Model{}
	}
	name := strings.TrimSpace(c.Roles.Main)
	if name == "" {
		name = builtinModel
		c.Roles.Main = name
	}
	m := c.Models[name]
	if m.Endpoint == "" && name == builtinModel {
		m.Endpoint = "http://localhost:8000/v1"
	}
	fn(&m)
	c.Models[name] = m
}

func (c *Config) editFast(fn func(*Model)) {
	if c.Models == nil {
		c.Models = map[string]Model{}
	}
	name := strings.TrimSpace(c.Roles.Fast)
	if name == "" {
		name = "fast"
	}
	m := c.Models[name]
	fn(&m)
	c.Models[name] = m
}

func applyModelMap(c *Config, n *yaml.Node, full string, mentioned map[string]bool) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyModelMap(c, n.Alias, full, mentioned)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s must be a mapping (line %d)", full, n.Line)
	}
	if c != nil && c.Models == nil {
		c.Models = map[string]Model{}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		name := kn.Value
		if !validModelName(name) {
			return fmt.Errorf("config: %s.%s is not a valid model name (line %d)", full, name, kn.Line)
		}
		if mentioned != nil {
			mentioned[name] = true
		}
		var m Model
		if c != nil {
			m = c.Models[name]
		}
		if err := applyModelFields(&m, vn, full+"."+name); err != nil {
			return err
		}
		if c != nil {
			c.Models[name] = m
		}
	}
	return nil
}

func applyModelFields(m *Model, n *yaml.Node, full string) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyModelFields(m, n.Alias, full)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s must be a mapping (line %d)", full, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		switch kn.Value {
		case "endpoint":
			if vn.Kind != yaml.ScalarNode {
				return fmt.Errorf("config: %s.endpoint must be a string (line %d)", full, vn.Line)
			}
			m.Endpoint = vn.Value
		case "model":
			if vn.Kind != yaml.ScalarNode {
				return fmt.Errorf("config: %s.model must be a string (line %d)", full, vn.Line)
			}
			m.Model = vn.Value
		case "thinking_kwargs":
			b, err := parseBool(vn, full+".thinking_kwargs")
			if err != nil {
				return err
			}
			m.ThinkingKwargs = b
		case "context":
			v, err := parseInt(vn, full+".context")
			if err != nil {
				return err
			}
			if v < 0 {
				return fmt.Errorf("config: %s.context must be >= 0, got %d", full, v)
			}
			m.Context = v
		case "notes":
			if vn.Kind != yaml.ScalarNode {
				return fmt.Errorf("config: %s.notes must be a string (line %d)", full, vn.Line)
			}
			m.Notes = vn.Value
		default:
			return fmt.Errorf("unknown key %q at line %d", full+"."+kn.Value, kn.Line)
		}
	}
	return nil
}

func validateModels(c Config) error {
	if len(c.Models) == 0 {
		return fmt.Errorf("config: models: at least one model is required")
	}
	names := c.ModelNames()
	main := strings.TrimSpace(c.Roles.Main)
	if main == "" {
		return fmt.Errorf("config: roles.main is empty; valid names: %s", strings.Join(names, ", "))
	}
	m, ok := c.Models[main]
	if !ok {
		return fmt.Errorf("config: roles.main %q is not a configured model; valid names: %s", main, strings.Join(names, ", "))
	}
	if err := requireURL("models."+main+".endpoint", m.Endpoint); err != nil {
		return err
	}
	if m.Context < 0 {
		return fmt.Errorf("config: models.%s.context must be >= 0, got %d", main, m.Context)
	}
	if fast := strings.TrimSpace(c.Roles.Fast); fast != "" {
		fm, ok := c.Models[fast]
		if !ok {
			return fmt.Errorf("config: roles.fast %q is not a configured model; valid names: %s", fast, strings.Join(names, ", "))
		}
		if err := requireURL("models."+fast+".endpoint", fm.Endpoint); err != nil {
			return err
		}
	}
	if err := optionalURL("roles.embed", c.Roles.Embed); err != nil {
		return err
	}
	if err := optionalURL("roles.rerank", c.Roles.Rerank); err != nil {
		return err
	}
	return nil
}

func optionalURL(name, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return requireURL(name, raw)
}

func pruneBuiltinModel(c *Config, mentioned map[string]bool, saw bool) {
	if c == nil || !saw || mentioned["default"] {
		return
	}
	if c.Roles.Main == builtinModel || c.Roles.Fast == builtinModel {
		return
	}
	m, ok := c.Models[builtinModel]
	if !ok {
		return
	}
	if m.Endpoint == "http://localhost:8000/v1" && m.Model == "" && !m.ThinkingKwargs && m.Context == 0 && m.Notes == "" {
		delete(c.Models, builtinModel)
	}
}

func applyModelEnv(c *Config, src map[string]string) error {
	if v, ok := os.LookupEnv("KONSOLAI_ENDPOINT"); ok && strings.TrimSpace(v) != "" {
		if err := requireURL("KONSOLAI_ENDPOINT", v); err != nil {
			return err
		}
		c.editMain(func(m *Model) { m.Endpoint = v })
		noteModelEnv(src, "models."+c.Roles.Main+".endpoint")
	}
	if v, ok := os.LookupEnv("KONSOLAI_MODEL"); ok && v != "" {
		c.editMain(func(m *Model) { m.Model = v })
		noteModelEnv(src, "models."+c.Roles.Main+".model")
	}
	if v, ok := os.LookupEnv("KONSOLAI_FAST_ENDPOINT"); ok && strings.TrimSpace(v) != "" {
		if err := requireURL("KONSOLAI_FAST_ENDPOINT", v); err != nil {
			return err
		}
		c.editFast(func(m *Model) { m.Endpoint = v })
	}
	if v, ok := os.LookupEnv("KONSOLAI_FAST_MODEL"); ok && v != "" {
		c.editFast(func(m *Model) { m.Model = v })
	}
	if v, ok := os.LookupEnv("KONSOLAI_EMBED_ENDPOINT"); ok && strings.TrimSpace(v) != "" {
		if err := optionalURL("KONSOLAI_EMBED_ENDPOINT", v); err != nil {
			return err
		}
		c.Roles.Embed = v
		if src != nil {
			src["roles.embed"] = "env"
		}
	}
	if v, ok := os.LookupEnv("KONSOLAI_RERANK_ENDPOINT"); ok && strings.TrimSpace(v) != "" {
		if err := optionalURL("KONSOLAI_RERANK_ENDPOINT", v); err != nil {
			return err
		}
		c.Roles.Rerank = v
		if src != nil {
			src["roles.rerank"] = "env"
		}
	}
	return nil
}

func noteModelEnv(src map[string]string, path string) {
	if src != nil {
		src[path] = "env"
	}
}

// migrateLegacyShape rewrites llm/fast/rag endpoint keys into models and roles.
func migrateLegacyShape(n *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	migrateLLM(n)
	migrateFast(n)
	migrateRAG(n)
	migrateBuildThinking(n)
}

func migrateLLM(n *yaml.Node) {
	llm := mapValue(n, "llm")
	if llm == nil || llm.Kind != yaml.MappingNode {
		return
	}
	ep := scalarOf(mapValue(llm, "endpoint"))
	model := scalarOf(mapValue(llm, "model"))
	think := mapValue(llm, "thinking_kwargs")
	if ep == "" && model == "" && think == nil {
		return
	}
	models := ensureMap(n, "models")
	def := ensureMap(models, builtinModel)
	if ep != "" && scalarOf(mapValue(def, "endpoint")) == "" {
		setScalar(def, "endpoint", ep)
	}
	if model != "" && mapValue(def, "model") == nil {
		setScalar(def, "model", model)
	}
	if think != nil && mapValue(def, "thinking_kwargs") == nil {
		setScalar(def, "thinking_kwargs", think.Value)
	}
	deleteKey(llm, "endpoint")
	deleteKey(llm, "model")
	deleteKey(llm, "thinking_kwargs")
	roles := ensureMap(n, "roles")
	if mapValue(roles, "main") == nil {
		setScalar(roles, "main", builtinModel)
	}
}

func migrateFast(n *yaml.Node) {
	fast := mapValue(n, "fast")
	if fast == nil || fast.Kind != yaml.MappingNode {
		return
	}
	ep := scalarOf(mapValue(fast, "endpoint"))
	model := scalarOf(mapValue(fast, "model"))
	think := mapValue(fast, "thinking_kwargs")
	en := mapValue(fast, "enabled")
	if ep == "" && model == "" && think == nil && en == nil {
		return
	}
	enabled := true
	if en != nil && strings.EqualFold(strings.TrimSpace(en.Value), "false") {
		enabled = false
	}
	if enabled && ep != "" {
		models := ensureMap(n, "models")
		fm := ensureMap(models, "fast")
		if scalarOf(mapValue(fm, "endpoint")) == "" {
			setScalar(fm, "endpoint", ep)
		}
		if model != "" && mapValue(fm, "model") == nil {
			setScalar(fm, "model", model)
		}
		if think != nil && mapValue(fm, "thinking_kwargs") == nil {
			setScalar(fm, "thinking_kwargs", think.Value)
		}
		roles := ensureMap(n, "roles")
		if mapValue(roles, "fast") == nil {
			setScalar(roles, "fast", "fast")
		}
	}
	deleteKey(fast, "endpoint")
	deleteKey(fast, "model")
	deleteKey(fast, "thinking_kwargs")
	deleteKey(fast, "enabled")
}

func migrateRAG(n *yaml.Node) {
	rag := mapValue(n, "rag")
	if rag == nil || rag.Kind != yaml.MappingNode {
		return
	}
	if ep := scalarOf(mapValue(rag, "embed_endpoint")); ep != "" {
		roles := ensureMap(n, "roles")
		if mapValue(roles, "embed") == nil {
			setScalar(roles, "embed", ep)
		}
		deleteKey(rag, "embed_endpoint")
	}
	if ep := scalarOf(mapValue(rag, "rerank_endpoint")); ep != "" {
		roles := ensureMap(n, "roles")
		if mapValue(roles, "rerank") == nil {
			setScalar(roles, "rerank", ep)
		}
		deleteKey(rag, "rerank_endpoint")
	}
}

func migrateBuildThinking(n *yaml.Node) {
	agent := mapValue(n, "agent")
	if agent == nil || agent.Kind != yaml.MappingNode {
		return
	}
	bt := mapValue(agent, "build_thinking")
	if bt == nil || bt.Kind != yaml.ScalarNode {
		return
	}
	if mapValue(agent, "think_act") == nil {
		setScalar(agent, "think_act", bt.Value)
	}
}

func writeModels(b *strings.Builder, c Config, indent int) {
	pad := strings.Repeat("  ", indent)
	fmt.Fprintf(b, "%smodels:\n", pad)
	names := c.ModelNames()
	if len(names) == 0 {
		names = []string{builtinModel}
	}
	sort.Strings(names)
	for _, name := range names {
		m := c.Models[name]
		if c.Models == nil && name == builtinModel {
			m = Model{Endpoint: "http://localhost:8000/v1"}
		}
		fmt.Fprintf(b, "%s  %s:\n", pad, name)
		fmt.Fprintf(b, "%s    endpoint: %s   # OpenAI-compatible API base\n", pad, yamlScalar(m.Endpoint))
		fmt.Fprintf(b, "%s    model: %s   # empty uses the server default\n", pad, yamlScalar(m.Model))
		fmt.Fprintf(b, "%s    thinking_kwargs: %s   # send chat_template_kwargs.enable_thinking\n", pad, strconv.FormatBool(m.ThinkingKwargs))
		fmt.Fprintf(b, "%s    context: %d   # 0 uses the server context window\n", pad, m.Context)
		fmt.Fprintf(b, "%s    notes: %s   # optional note\n", pad, yamlScalar(m.Notes))
	}
}

func urlHasPassword(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			return false
		}
		_, ok := u.User.Password()
		return ok
	}
	for _, field := range strings.Fields(raw) {
		k, v, ok := strings.Cut(field, "=")
		if ok && strings.EqualFold(k, "password") && v != "" {
			return true
		}
	}
	return false
}

func writeModelDiff(b *strings.Builder, base, fileCfg, full Config) {
	names := map[string]struct{}{}
	for _, n := range base.ModelNames() {
		names[n] = struct{}{}
	}
	for _, n := range full.ModelNames() {
		names[n] = struct{}{}
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	fields := []struct {
		name string
		get  func(Model) string
	}{
		{"endpoint", func(m Model) string { return m.Endpoint }},
		{"model", func(m Model) string { return m.Model }},
		{"thinking_kwargs", func(m Model) string { return strconv.FormatBool(m.ThinkingKwargs) }},
		{"context", func(m Model) string { return strconv.Itoa(m.Context) }},
		{"notes", func(m Model) string { return m.Notes }},
	}
	for _, name := range ordered {
		for _, f := range fields {
			dv := f.get(base.Models[name])
			fv := f.get(fileCfg.Models[name])
			ev := f.get(full.Models[name])
			if ev == dv {
				continue
			}
			source := "file"
			if ev != fv {
				source = "env"
			}
			fmt.Fprintf(b, "models.%s.%s  %s -> %s  (%s)\n", name, f.name, yamlScalar(dv), yamlScalar(ev), source)
		}
	}
}

func modelEnvLines() string {
	return "" +
		"# OpenAI-compatible API base for the main role\n" +
		"KONSOLAI_ENDPOINT=http://localhost:8000/v1\n" +
		"# empty uses the server default\n" +
		"KONSOLAI_MODEL=\n" +
		"# fast model API base; empty leaves the fast role unchanged\n" +
		"KONSOLAI_FAST_ENDPOINT=\n" +
		"# fast model id\n" +
		"KONSOLAI_FAST_MODEL=\n" +
		"# embedding service URL\n" +
		"KONSOLAI_EMBED_ENDPOINT=\n" +
		"# rerank service URL\n" +
		"KONSOLAI_RERANK_ENDPOINT=\n"
}
