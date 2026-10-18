package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestConfigKeysComplete(t *testing.T) {
	leaves := configLeaves(reflect.TypeOf(Config{}), reflect.ValueOf(Builtin()))
	byPath := map[string]Key{}
	for _, k := range keyTable {
		if k.Path == "" || k.Doc == "" {
			t.Fatalf("key missing path or doc: %+v", k.Path)
		}
		if _, dup := byPath[k.Path]; dup {
			t.Fatalf("duplicate key %s", k.Path)
		}
		byPath[k.Path] = k
		if err := k.touchesField(); err != nil {
			t.Error(err)
		}
	}
	seen := map[string]bool{}
	for _, leaf := range leaves {
		if leaf.goPath == "Name" {
			continue
		}
		seen[leaf.yamlPath] = true
		k, ok := byPath[leaf.yamlPath]
		if !ok {
			t.Errorf("%s has no table entry (%s)", leaf.goPath, leaf.yamlPath)
			continue
		}
		if k.Kind == 0 && k.SetStr == nil && leaf.typ.Kind() != reflect.String {
			t.Errorf("%s kind does not match %s", k.Path, leaf.typ)
		}
	}
	for path, k := range byPath {
		if !seen[path] {
			t.Errorf("table entry %s has no Config field", k.Path)
		}
	}
}

func (k Key) touchesField() error {
	c := Config{}
	switch k.Kind {
	case KindString:
		if k.SetStr == nil || k.GetStr == nil {
			return errKey(k.Path, "missing string accessor")
		}
		k.SetStr(&c, "sentinel-value")
		if k.GetStr(c) != "sentinel-value" {
			return errKey(k.Path, "string setter did not stick")
		}
	case KindBool:
		if k.SetBool == nil || k.GetBool == nil {
			return errKey(k.Path, "missing bool accessor")
		}
		k.SetBool(&c, true)
		if !k.GetBool(c) {
			return errKey(k.Path, "bool setter did not stick")
		}
	case KindInt, KindDurationMS:
		if k.SetInt == nil || k.GetInt == nil {
			return errKey(k.Path, "missing int accessor")
		}
		k.SetInt(&c, 4242)
		if k.GetInt(c) != 4242 {
			return errKey(k.Path, "int setter did not stick")
		}
	case KindFloat:
		if k.SetFloat == nil || k.GetFloat == nil {
			return errKey(k.Path, "missing float accessor")
		}
		k.SetFloat(&c, 0.25)
		if k.GetFloat(c) != 0.25 {
			return errKey(k.Path, "float setter did not stick")
		}
	case KindList:
		if k.SetList == nil || k.GetList == nil {
			return errKey(k.Path, "missing list accessor")
		}
		k.SetList(&c, []string{"sentinel-item"})
		if got := k.GetList(c); len(got) != 1 || got[0] != "sentinel-item" {
			return errKey(k.Path, "list setter did not stick")
		}
	default:
		return errKey(k.Path, "unknown kind")
	}
	return nil
}

type keyErr string

func (e keyErr) Error() string { return string(e) }

func errKey(path, msg string) error {
	return keyErr(path + ": " + msg)
}

func TestInitYAMLMatchesBuiltin(t *testing.T) {
	clearEnv(t)
	f, err := Parse([]byte(InitYAML()), "init.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Resolve("home")
	if err != nil {
		t.Fatal(err)
	}
	want := Builtin()
	got.Name = want.Name
	for _, k := range keyTable {
		if k.format(got) != k.format(want) {
			t.Errorf("%s init %s builtin %s", k.Path, k.format(got), k.format(want))
		}
	}
}

func TestLegacyPaletteAlias(t *testing.T) {
	clearEnv(t)
	raw := "defaults:\n  ui:\n    palette: ocean\n"
	f, err := Parse([]byte(raw), "palette.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.Theme != "glacier" || c.PaletteName() != "glacier" {
		t.Fatalf("theme %q", c.UI.Theme)
	}
}

func TestEveryEnvOverrides(t *testing.T) {
	clearEnv(t)
	samples := map[string]string{}
	for _, k := range keyTable {
		if k.Env == "" {
			continue
		}
		samples[k.Env] = envSample(k)
		t.Setenv(k.Env, samples[k.Env])
	}
	c, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keyTable {
		if k.Env == "" {
			continue
		}
		if err := k.setFromString(&Config{}, samples[k.Env]); err != nil {
			t.Fatal(err)
		}
		probe := Config{}
		if err := k.setFromString(&probe, samples[k.Env]); err != nil {
			t.Fatal(err)
		}
		if k.format(probe) != k.format(c) && k.Path != "language.default" {
			// language.default is not an env key
		}
		if k.present(c, false) != k.present(probe, false) {
			t.Errorf("%s env %s got %s want %s", k.Env, k.Path, k.present(c, false), k.present(probe, false))
		}
	}
}

func envSample(k Key) string {
	switch k.Path {
	case "database.url":
		return "postgres://env.example/db"
	case "agent.build_thinking":
		return "until_plan"
	case "llm.model", "fast.model":
		return "env-model"
	default:
		if k.Check == CheckURL || k.Check == CheckURLOptional {
			return "http://env.example/v1"
		}
		return "env-value"
	}
}

func TestShowAndDiffMaskSecret(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := dir + "/config.yaml"
	body := "default_profile: home\nprofiles:\n  home:\n    database:\n      url: postgres://user:hunter2@localhost:5432/konsolai\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("home")
	if err != nil {
		t.Fatal(err)
	}
	shown, err := c.ShowYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shown, "hunter2") || !strings.Contains(shown, "***") {
		t.Fatalf("show leaked or missed mask:\n%s", shown)
	}
	diff, err := DiffText(path, "home")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "hunter2") || !strings.Contains(diff, "***") {
		t.Fatalf("diff leaked or missed mask:\n%s", diff)
	}
	if !strings.Contains(diff, "(file)") {
		t.Fatalf("diff source:\n%s", diff)
	}
}

func configLeaves(t reflect.Type, v reflect.Value) []configLeaf {
	var out []configLeaf
	var walk func(t reflect.Type, v reflect.Value, goPath, yamlPath string)
	walk = func(t reflect.Type, v reflect.Value, goPath, yamlPath string) {
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
			if v.IsValid() && !v.IsNil() {
				v = v.Elem()
			} else {
				v = reflect.Zero(t)
			}
		}
		if t.Kind() != reflect.Struct {
			out = append(out, configLeaf{goPath: goPath, yamlPath: yamlPath, typ: t})
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, omit := yamlName(f)
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
			fv := reflect.Zero(ft)
			if v.IsValid() && v.Kind() == reflect.Struct {
				fv = v.Field(i)
			}
			if ft.Kind() == reflect.Struct && !omit {
				walk(ft, fv, nextGo, nextYAML)
				continue
			}
			out = append(out, configLeaf{goPath: nextGo, yamlPath: nextYAML, typ: ft})
		}
	}
	walk(t, v, "", "")
	return out
}

func yamlName(f reflect.StructField) (string, bool) {
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

type configLeaf struct {
	goPath   string
	yamlPath string
	typ      reflect.Type
}

func TestEnvBeatsFileInDiff(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := dir + "/config.yaml"
	body := "defaults:\n  llm:\n    model: from-file\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_MODEL", "from-env")
	diff, err := DiffText(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "llm.model") || !strings.Contains(diff, "from-env") || !strings.Contains(diff, "(env)") {
		t.Fatalf("diff:\n%s", diff)
	}
	if strings.Contains(diff, "(flag)") {
		t.Fatalf("unexpected flag source:\n%s", diff)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(diff), &doc); err == nil && doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		t.Fatal("diff should be plain lines, not a mapping document only")
	}
}
