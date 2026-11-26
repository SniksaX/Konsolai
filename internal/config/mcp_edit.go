package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// UpsertMCPServer writes one server under defaults.mcp.servers, keeping the
// comments and order of the rest of the file. remove drops the server.
func UpsertMCPServer(path, name string, server MCPServer, remove bool) error {
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
	mcp := ensureMap(defaults, "mcp")
	servers := ensureMap(mcp, "servers")
	if remove {
		deleteKey(servers, name)
	} else {
		body := ensureMap(servers, name)
		writeMCPServerNode(body, server)
	}
	return encodeAtomic(&doc, path)
}

// writeMCPServerNode replaces every managed field of one server node.
func writeMCPServerNode(body *yaml.Node, server MCPServer) {
	setMCPScalar(body, "transport", server.Transport, "!!str")
	setMCPScalar(body, "enabled", boolText(server.Enabled), "!!bool")
	setMCPScalar(body, "trust", server.TrustMode(), "!!str")
	setMCPScalar(body, "timeout_ms", strconv.Itoa(server.Timeout()), "!!int")
	setMCPScalar(body, "private", boolText(server.Private), "!!bool")
	if server.Command == "" {
		deleteKey(body, "command")
	} else {
		setMCPScalar(body, "command", server.Command, "!!str")
	}
	if server.URL == "" {
		deleteKey(body, "url")
	} else {
		setMCPScalar(body, "url", server.URL, "!!str")
	}
	setMCPList(body, "args", server.Args)
	setMCPStringMap(body, "env", server.Env)
	setMCPStringMap(body, "headers", server.Headers)
	if len(server.Tools.Allow) == 0 && len(server.Tools.Deny) == 0 && len(server.Tools.ReadOnly) == 0 {
		deleteKey(body, "tools")
		return
	}
	tools := ensureMap(body, "tools")
	setMCPList(tools, "allow", server.Tools.Allow)
	setMCPList(tools, "deny", server.Tools.Deny)
	setMCPList(tools, "read_only", server.Tools.ReadOnly)
}

func setMCPScalar(m *yaml.Node, key, value, tag string) {
	if v := mapValue(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = tag
		v.Value = value
		v.Content = nil
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}

func setMCPList(m *yaml.Node, key string, items []string) {
	if len(items) == 0 {
		deleteKey(m, key)
		return
	}
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, item := range items {
		node.Content = append(node.Content, scalarNode(item))
	}
	if v := mapValue(m, key); v != nil {
		keep := v
		keep.Kind = node.Kind
		keep.Tag = node.Tag
		keep.Value = ""
		keep.Content = node.Content
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
}

func setMCPStringMap(m *yaml.Node, key string, values map[string]string) {
	if len(values) == 0 {
		deleteKey(m, key)
		return
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range sortedKeys(values) {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: values[name]})
	}
	if v := mapValue(m, key); v != nil {
		v.Kind = node.Kind
		v.Tag = node.Tag
		v.Value = ""
		v.Content = node.Content
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, node)
}
