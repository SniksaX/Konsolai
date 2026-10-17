package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ThemeNames is the canonical ui.theme set, amber first (default).
var ThemeNames = []string{"amber", "glacier", "nebula", "jade", "rose", "mono"}

var themeAliases = map[string]string{
	"amber":   "amber",
	"glacier": "glacier",
	"cyan":    "glacier",
	"ocean":   "glacier",
	"nebula":  "nebula",
	"violet":  "nebula",
	"jade":    "jade",
	"green":   "jade",
	"forest":  "jade",
	"rose":    "rose",
	"red":     "rose",
	"mono":    "mono",
}

func ResolveTheme(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	canon, ok := themeAliases[name]
	return canon, ok
}

func isOldAppearance(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "auto", "dark", "light":
		return true
	default:
		return false
	}
}

// SetTheme writes defaults.ui.theme through the yaml Node API so
// comments and key order in path are left intact. Aliases are resolved.
func SetTheme(path, name string) error {
	canon, ok := ResolveTheme(name)
	if !ok {
		return fmt.Errorf("config: ui.theme must be amber, glacier, nebula, jade, rose or mono, got %q", name)
	}
	return writeUIScalar(path, "theme", canon)
}

// SetPalette writes the resolved theme name to defaults.ui.theme.
// Kept so callers that still say "palette" persist the new key.
func SetPalette(path, name string) error {
	return SetTheme(path, name)
}

func writeUIScalar(path, key, value string) error {
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("config: read %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("config: mkdir: %w", err)
		}
		body := "defaults:\n  ui:\n    " + key + ": " + value + "\n"
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			return fmt.Errorf("config: write %s: %w", path, err)
		}
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("config: decode %s: %w", path, err)
	}
	root := mappingRoot(&doc)
	if root == nil {
		return fmt.Errorf("config: %s: expected a mapping", path)
	}
	defaults := ensureMap(root, "defaults")
	ui := ensureMap(defaults, "ui")
	setScalar(ui, key, value)
	return encodeYAML(path, &doc)
}

func migrateUIKeys(path string, b []byte) (bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return false, fmt.Errorf("config: decode %s: %w", path, err)
	}
	root := mappingRoot(&doc)
	if root == nil {
		return false, nil
	}
	changed := false
	if ui := uiMap(root, "defaults"); ui != nil {
		if migrateUIMap(ui) {
			changed = true
		}
	}
	if profiles := mapValue(root, "profiles"); profiles != nil && profiles.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(profiles.Content); i += 2 {
			if ui := uiMap(profiles.Content[i+1], ""); ui != nil {
				if migrateUIMap(ui) {
					changed = true
				}
			}
		}
	}
	if !changed {
		return false, nil
	}
	if err := encodeYAML(path, &doc); err != nil {
		return false, err
	}
	return true, nil
}

func uiMap(parent *yaml.Node, nest string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	n := parent
	if nest != "" {
		n = mapValue(parent, nest)
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
	}
	ui := mapValue(n, "ui")
	if ui == nil || ui.Kind != yaml.MappingNode {
		return nil
	}
	return ui
}

func migrateUIMap(ui *yaml.Node) bool {
	themeN := mapValue(ui, "theme")
	palN := mapValue(ui, "palette")
	appearN := mapValue(ui, "appearance")
	themeVal := scalarOf(themeN)
	palVal := scalarOf(palN)
	changed := false

	if isOldAppearance(themeVal) {
		if appearN == nil {
			setScalar(ui, "appearance", strings.ToLower(strings.TrimSpace(themeVal)))
			changed = true
		}
		if palVal == "" {
			setScalar(ui, "theme", "amber")
			changed = true
		}
	}
	if palN != nil {
		if name, ok := ResolveTheme(palVal); ok {
			setScalar(ui, "theme", name)
		} else if !isOldAppearance(themeVal) && themeVal == "" {
			setScalar(ui, "theme", "amber")
		}
		deleteKey(ui, "palette")
		changed = true
	}
	return changed
}

func scalarOf(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func encodeYAML(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	mode := os.FileMode(0600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(path, buf.Bytes(), mode); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

func mappingRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			m := &yaml.Node{Kind: yaml.MappingNode}
			doc.Content = []*yaml.Node{m}
			return m
		}
		if doc.Content[0].Kind == yaml.MappingNode {
			return doc.Content[0]
		}
		return nil
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func ensureMap(parent *yaml.Node, key string) *yaml.Node {
	if v := mapValue(parent, key); v != nil {
		if v.Kind == yaml.MappingNode {
			return v
		}
		v.Kind = yaml.MappingNode
		v.Tag = "!!map"
		v.Value = ""
		v.Content = nil
		return v
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, k, v)
	return v
}

func setScalar(m *yaml.Node, key, value string) {
	if v := mapValue(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		return
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	m.Content = append(m.Content, k, v)
}

func deleteKey(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
