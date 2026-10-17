package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SetPalette writes defaults.ui.palette through the yaml Node API so
// comments and key order in path are left intact.
func SetPalette(path, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "amber", "violet", "ocean", "forest", "rose", "mono":
	default:
		return fmt.Errorf("config: ui.palette must be amber, violet, ocean, forest, rose or mono, got %q", name)
	}
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
		body := "defaults:\n  ui:\n    palette: " + name + "\n"
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
	setScalar(ui, "palette", name)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
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
