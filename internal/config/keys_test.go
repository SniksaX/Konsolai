package config

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// keyException skips a completeness check for one Config leaf.
// Path is the dotted Go field path from Config.
type keyException struct {
	path    string
	schema  bool
	load    bool
	builtin bool
	init    bool
	reason  string
}

// keyExceptions are intentional gaps in the config-key checklist.
// Every other leaf must be accepted by the schema, round-trip through the
// loader overlay, be non-zero in Builtin, and appear in InitYAML.
var keyExceptions = []keyException{
	{
		path: "Name", schema: true, load: true, init: true,
		reason: "profile name is assigned when a profile is resolved, not stored as its own key",
	},
	{
		path: "DatabaseURL", schema: true, load: true, builtin: true, init: true,
		reason: "flat mirror of database.url; builtin stays empty so memory is off until a URL is set",
	},
	{
		path: "Endpoint", schema: true, load: true, init: true,
		reason: "flat mirror of llm.endpoint, copied by syncFlat",
	},
	{
		path: "Model", schema: true, load: true, builtin: true, init: true,
		reason: "flat mirror of llm.model; empty selects the server default",
	},
	{
		path: "FastEndpoint", schema: true, load: true, init: true,
		reason: "flat mirror of fast.endpoint, copied by syncFlat",
	},
	{
		path: "FastModel", schema: true, load: true, builtin: true, init: true,
		reason: "flat mirror of fast.model; empty selects the server default",
	},
	{
		path: "SessionIdleMinutes", schema: true, load: true, init: true,
		reason: "flat mirror of memory.session_idle_minutes, copied by syncFlat",
	},
	{
		path: "RetentionDays", schema: true, load: true, init: true,
		reason: "flat mirror of memory.retention_days, copied by syncFlat",
	},
	{
		path: "LLMIdleTimeoutSeconds", schema: true, load: true, init: true,
		reason: "flat mirror of llm.idle_timeout_seconds, copied by syncFlat",
	},
	{
		path: "BuildThinking", schema: true, load: true, init: true,
		reason: "flat mirror of agent.build_thinking, copied by syncFlat",
	},
	{
		path: "Languages", schema: true, load: true, init: true,
		reason: "flat mirror of language.languages, copied by syncFlat",
	},
	{
		path: "DefaultLanguage", schema: true, load: true, init: true,
		reason: "flat mirror of language.default, copied by syncFlat",
	},
	{
		path: "EmbedEndpoint", schema: true, load: true, init: true,
		reason: "flat mirror of rag.embed_endpoint, copied by syncFlat",
	},
	{
		path: "RerankEndpoint", schema: true, load: true, init: true,
		reason: "flat mirror of rag.rerank_endpoint, copied by syncFlat",
	},
	{
		path: "MemoryRAG", schema: true, load: true, init: true,
		reason: "flat mirror of rag.enabled, copied by syncFlat",
	},
	{
		path: "LLM.Model", builtin: true,
		reason: "empty model name means the server default",
	},
	{
		path: "Fast.Model", builtin: true,
		reason: "empty fast model name means the server default",
	},
	{
		path: "Database.URL", builtin: true,
		reason: "empty database URL leaves memory disabled",
	},
	{
		path: "UI.Palette", builtin: true, init: true,
		reason: "legacy theme alias still accepted on load; builtin leaves it empty and the sample file does not write it",
	},
}

func TestConfigKeysComplete(t *testing.T) {
	exc := map[string]keyException{}
	for _, e := range keyExceptions {
		if e.path == "" || e.reason == "" {
			t.Fatalf("allowlist entry missing path or reason: %+v", e)
		}
		if !e.schema && !e.load && !e.builtin && !e.init {
			t.Fatalf("allowlist %s skips nothing", e.path)
		}
		if _, dup := exc[e.path]; dup {
			t.Fatalf("duplicate allowlist %s", e.path)
		}
		exc[e.path] = e
	}

	leaves := configLeaves(reflect.TypeOf(Config{}), reflect.ValueOf(Builtin()))
	seen := map[string]bool{}
	initDoc := parseInit(t)
	for _, leaf := range leaves {
		seen[leaf.goPath] = true
		e := exc[leaf.goPath]
		if leaf.yamlPath == "" && !e.schema {
			t.Errorf("%s has no yaml key and is not allowlisted", leaf.goPath)
		}
		if !e.schema && !schemaAccepts(t, leaf.yamlPath) {
			t.Errorf("%s: schema rejects %s", leaf.goPath, leaf.yamlPath)
		}
		if !e.load && !overlaySets(t, leaf) {
			t.Errorf("%s: loader overlay did not round-trip %s", leaf.goPath, leaf.yamlPath)
		}
		if !e.builtin && leaf.builtin.IsZero() {
			t.Errorf("%s: Builtin leaves it zero", leaf.goPath)
		}
		if !e.init && !initHas(initDoc, leaf.yamlPath) {
			t.Errorf("%s: InitYAML missing %s", leaf.goPath, leaf.yamlPath)
		}
	}
	for path := range exc {
		if !seen[path] {
			t.Errorf("allowlist %s is not a Config leaf (%s)", path, exc[path].reason)
		}
	}
}

type configLeaf struct {
	goPath   string
	yamlPath string
	typ      reflect.Type
	builtin  reflect.Value
}

func configLeaves(t reflect.Type, v reflect.Value) []configLeaf {
	var out []configLeaf
	var walk func(t reflect.Type, v reflect.Value, goPath, yamlPath string)
	walk = func(t reflect.Type, v reflect.Value, goPath, yamlPath string) {
		if t.Kind() != reflect.Struct {
			out = append(out, configLeaf{goPath: goPath, yamlPath: yamlPath, typ: t, builtin: v})
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, omit := yamlKey(f)
			nextGo := f.Name
			if goPath != "" {
				nextGo = goPath + "." + f.Name
			}
			nextYAML := yamlPath
			if !omit && name != "" {
				if yamlPath == "" {
					nextYAML = name
				} else {
					nextYAML = yamlPath + "." + name
				}
			}
			ft := f.Type
			fv := v.Field(i)
			if ft.Kind() == reflect.Struct && !omit {
				walk(ft, fv, nextGo, nextYAML)
				continue
			}
			out = append(out, configLeaf{goPath: nextGo, yamlPath: nextYAML, typ: ft, builtin: fv})
		}
	}
	walk(t, v, "", "")
	return out
}

func yamlKey(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("yaml")
	if tag == "-" {
		return "", true
	}
	if tag == "" {
		return strings.ToLower(f.Name), false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return "", true
	}
	return name, false
}

func schemaAccepts(t *testing.T, yamlPath string) bool {
	t.Helper()
	raw := nestYAML(t, yamlPath, sentinelYAML(reflect.TypeOf("")))
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema yaml %s: %v", yamlPath, err)
	}
	return checkKnown(&doc, overlaySchema, "") == nil
}

func overlaySets(t *testing.T, leaf configLeaf) bool {
	t.Helper()
	sample, want, ok := sentinel(leaf.typ)
	if !ok {
		t.Fatalf("%s: unsupported leaf type %s", leaf.goPath, leaf.typ)
	}
	raw := nestYAML(t, leaf.yamlPath, sample)
	wrapped := append([]byte("defaults:\n"), indentYAML(raw)...)
	var f File
	if err := yaml.Unmarshal(wrapped, &f); err != nil {
		t.Fatalf("%s: decode overlay: %v\n%s", leaf.goPath, err, wrapped)
	}
	c := Config{}
	applyOverlay(&c, f.Defaults)
	got := fieldByPath(reflect.ValueOf(c), leaf.goPath)
	if !got.IsValid() {
		t.Fatalf("%s: missing field", leaf.goPath)
	}
	return reflect.DeepEqual(got.Interface(), want.Interface())
}

func sentinel(t reflect.Type) (yamlVal any, cmp reflect.Value, ok bool) {
	switch t.Kind() {
	case reflect.Bool:
		v := reflect.New(t).Elem()
		v.SetBool(true)
		return true, v, true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v := reflect.New(t).Elem()
		v.SetInt(4242)
		return int(4242), v, true
	case reflect.Float32, reflect.Float64:
		v := reflect.New(t).Elem()
		v.SetFloat(0.25)
		return 0.25, v, true
	case reflect.String:
		v := reflect.New(t).Elem()
		v.SetString("sentinel-value")
		return "sentinel-value", v, true
	case reflect.Slice:
		if t.Elem().Kind() != reflect.String {
			return nil, reflect.Value{}, false
		}
		v := reflect.MakeSlice(t, 1, 1)
		v.Index(0).SetString("sentinel-item")
		return []string{"sentinel-item"}, v, true
	default:
		return nil, reflect.Value{}, false
	}
}

func sentinelYAML(t reflect.Type) any {
	v, _, ok := sentinel(t)
	if !ok {
		return "sentinel-value"
	}
	return v
}

func nestYAML(t *testing.T, yamlPath string, sample any) []byte {
	t.Helper()
	parts := strings.Split(yamlPath, ".")
	var cur any = sample
	for i := len(parts) - 1; i >= 0; i-- {
		cur = map[string]any{parts[i]: cur}
	}
	raw, err := yaml.Marshal(cur)
	if err != nil {
		t.Fatalf("marshal %s: %v", yamlPath, err)
	}
	return raw
}

func indentYAML(raw []byte) []byte {
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for i, ln := range lines {
		lines[i] = "  " + ln
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func fieldByPath(v reflect.Value, path string) reflect.Value {
	for _, name := range strings.Split(path, ".") {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.FieldByName(name)
		if !v.IsValid() {
			return v
		}
	}
	return v
}

func parseInit(t *testing.T) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(InitYAML()), &doc); err != nil {
		t.Fatalf("InitYAML: %v", err)
	}
	return &doc
}

func initHas(doc *yaml.Node, yamlPath string) bool {
	n := mapChild(doc, "defaults")
	if n == nil {
		return false
	}
	for _, part := range strings.Split(yamlPath, ".") {
		n = mapChild(n, part)
		if n == nil {
			return false
		}
	}
	return true
}

func mapChild(n *yaml.Node, key string) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return mapChild(n.Content[0], key)
	}
	if n.Kind == yaml.AliasNode {
		return mapChild(n.Alias, key)
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
