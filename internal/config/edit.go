package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SetFileKey writes path's value into the config file, preserving comments and order.
// remove drops the key so the built-in default applies.
func SetFileKey(path, keyPath, value string, remove bool) error {
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(b)) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("config: decode %s: %w", path, err)
	}
	root := mappingRoot(&doc)
	if root == nil {
		return fmt.Errorf("config: %s: expected a mapping", path)
	}
	defaults := ensureMap(root, "defaults")
	if err := putDotted(defaults, keyPath, value, remove); err != nil {
		return err
	}
	if profiles := mapValue(root, "profiles"); profiles != nil && profiles.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(profiles.Content); i += 2 {
			node := profiles.Content[i+1]
			if node == nil || node.Kind != yaml.MappingNode {
				continue
			}
			if !hasDotted(node, keyPath) {
				continue
			}
			_ = putDotted(node, keyPath, value, remove)
		}
	}
	return encodeAtomic(&doc, path)
}

func hasDotted(m *yaml.Node, keyPath string) bool {
	cur := m
	for _, part := range strings.Split(keyPath, ".") {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return false
		}
		cur = mapValue(cur, part)
		if cur == nil {
			return false
		}
	}
	return true
}

func putDotted(m *yaml.Node, keyPath, value string, remove bool) error {
	parts := strings.Split(keyPath, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("config: empty key")
	}
	cur := m
	for i := 0; i < len(parts)-1; i++ {
		if remove && mapValue(cur, parts[i]) == nil {
			return nil
		}
		next := ensureMap(cur, parts[i])
		cur = next
	}
	leaf := parts[len(parts)-1]
	if remove {
		deleteKey(cur, leaf)
		return nil
	}
	k := lookupKey(keyPath)
	if v := mapValue(cur, leaf); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = scalarTag(value)
		v.Value = value
		if k != nil && v.LineComment == "" {
			v.LineComment = k.Doc
		}
		return nil
	}
	kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: leaf}
	vn := &yaml.Node{Kind: yaml.ScalarNode, Tag: scalarTag(value), Value: value}
	if k != nil {
		vn.LineComment = k.Doc
	}
	cur.Content = append(cur.Content, kn, vn)
	return nil
}

func scalarTag(value string) string {
	switch strings.ToLower(value) {
	case "true", "false":
		return "!!bool"
	}
	if value != "" && value[0] >= '0' && value[0] <= '9' || (len(value) > 1 && value[0] == '-' && value[1] >= '0' && value[1] <= '9') {
		if !strings.ContainsAny(value, ".eE") && strings.Trim(value, "-0123456789") == "" {
			return "!!int"
		}
	}
	return "!!str"
}

func lookupKey(path string) *Key {
	for i := range keyTable {
		if keyTable[i].Path == path {
			return &keyTable[i]
		}
	}
	return nil
}

func encodeAtomic(doc *yaml.Node, path string) error {
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
	return WriteAtomic(path, buf.Bytes())
}

// UpsertModel writes one named model into defaults.models.
func UpsertModel(path, name string, m Model, remove bool) error {
	if path == "" {
		path = DefaultPath()
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(b)) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	} else if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("config: decode %s: %w", path, err)
	}
	root := mappingRoot(&doc)
	if root == nil {
		return fmt.Errorf("config: %s: expected a mapping", path)
	}
	defaults := ensureMap(root, "defaults")
	models := ensureMap(defaults, "models")
	if remove {
		deleteKey(models, name)
	} else {
		body := ensureMap(models, name)
		setScalar(body, "endpoint", m.Endpoint)
		setScalar(body, "model", m.Model)
		setScalar(body, "thinking_kwargs", boolText(m.ThinkingKwargs))
		setScalar(body, "context", fmt.Sprintf("%d", m.Context))
		setScalar(body, "notes", m.Notes)
	}
	return encodeAtomic(&doc, path)
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
