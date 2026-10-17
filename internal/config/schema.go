package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

type schemaKind int

const (
	schemaLeaf schemaKind = iota
	schemaMap
	schemaProfiles
)

type schema struct {
	kind schemaKind
	keys map[string]schema
}

func m(keys map[string]schema) schema {
	return schema{kind: schemaMap, keys: keys}
}

func leaf() schema { return schema{kind: schemaLeaf} }

var overlaySchema = m(map[string]schema{
	"llm": m(map[string]schema{
		"endpoint":             leaf(),
		"model":                leaf(),
		"idle_timeout_seconds": leaf(),
		"max_tokens": m(map[string]schema{
			"answer": leaf(),
			"lookup": leaf(),
			"act":    leaf(),
			"build":  leaf(),
		}),
	}),
	"fast": m(map[string]schema{
		"enabled":             leaf(),
		"endpoint":            leaf(),
		"model":               leaf(),
		"context_guard_ratio": leaf(),
	}),
	"database": m(map[string]schema{
		"url": leaf(),
	}),
	"memory": m(map[string]schema{
		"session_idle_minutes": leaf(),
		"retention_days":       leaf(),
		"compact_threshold":    leaf(),
	}),
	"context": m(map[string]schema{
		"elide_trigger": leaf(),
		"elide_target":  leaf(),
	}),
	"agent": m(map[string]schema{
		"max_steps": m(map[string]schema{
			"lookup": leaf(),
			"act":    leaf(),
			"build":  leaf(),
		}),
		"explore_budget": m(map[string]schema{
			"lookup":          leaf(),
			"lookup_analysis": leaf(),
			"act":             leaf(),
			"build":           leaf(),
			"build_docs":      leaf(),
			"build_analysis":  leaf(),
		}),
		"build_thinking": leaf(),
	}),
	"rag": m(map[string]schema{
		"enabled":         leaf(),
		"embed_endpoint":  leaf(),
		"rerank_endpoint": leaf(),
		"embed_batch":     leaf(),
		"chunk": m(map[string]schema{
			"turn_tokens":            leaf(),
			"web_tokens":             leaf(),
			"web_overlap_tokens":     leaf(),
			"code_window_lines":      leaf(),
			"code_overlap_lines":     leaf(),
			"go_max_decl_lines":      leaf(),
			"go_split_window_lines":  leaf(),
			"go_split_overlap_lines": leaf(),
		}),
		"search": m(map[string]schema{
			"vector_top":       leaf(),
			"text_top":         leaf(),
			"rrf_k":            leaf(),
			"rerank_top":       leaf(),
			"k":                leaf(),
			"rerank_threshold": leaf(),
		}),
		"index": m(map[string]schema{
			"max_file_kb":          leaf(),
			"confirm_over_minutes": leaf(),
		}),
	}),
	"ui": m(map[string]schema{
		"display_lines":    leaf(),
		"paste_chip_lines": leaf(),
		"paste_chip_chars": leaf(),
		"output_keep_kb":   leaf(),
		"palette":          leaf(),
		"banner":           leaf(),
		"theme":            leaf(),
		"code_highlight":   leaf(),
		"clear_on_start":   leaf(),
		"strip_emoji":      leaf(),
	}),
	"language": m(map[string]schema{
		"languages": leaf(),
		"default":   leaf(),
	}),
	"safety": m(map[string]schema{
		"extra_read_deny":    leaf(),
		"extra_command_deny": leaf(),
	}),
	"router": m(map[string]schema{
		"action_verbs": leaf(),
		"targets":      leaf(),
	}),
})

var rootSchema = m(map[string]schema{
	"default_profile": leaf(),
	"defaults":        overlaySchema,
	"profiles":        {kind: schemaProfiles},
})

func checkKnown(n *yaml.Node, sch schema, path string) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return checkKnown(n.Content[0], sch, path)
	}
	if n.Kind == yaml.AliasNode {
		return checkKnown(n.Alias, sch, path)
	}
	if n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || n.Value == "") {
		return nil
	}
	switch sch.kind {
	case schemaLeaf:
		return nil
	case schemaProfiles:
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("unknown key %q at line %d", path, n.Line)
		}
		for i := 0; i < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			child := key.Value
			if path != "" {
				child = path + "." + key.Value
			}
			if err := checkKnown(val, overlaySchema, child); err != nil {
				return err
			}
		}
		return nil
	default:
		if n.Kind != yaml.MappingNode {
			if path == "" {
				return fmt.Errorf("root must be a mapping (line %d)", n.Line)
			}
			return fmt.Errorf("%s must be a mapping (line %d)", path, n.Line)
		}
		for i := 0; i < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			sub, ok := sch.keys[key.Value]
			if !ok {
				name := key.Value
				if path != "" {
					name = path + "." + key.Value
				}
				return fmt.Errorf("unknown key %q at line %d", name, key.Line)
			}
			child := key.Value
			if path != "" {
				child = path + "." + key.Value
			}
			if err := checkKnown(val, sub, child); err != nil {
				return err
			}
		}
		return nil
	}
}
