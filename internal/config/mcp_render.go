package config

import (
	"fmt"
	"sort"
	"strings"
)

// writeMCP renders the mcp section for config.example.yaml, /config show and
// /config init --from-current. Values from env and headers are masked when
// mask is set; a ${ENV_VAR} reference is shown as written because it holds no
// secret.
func writeMCP(b *strings.Builder, c Config, mask bool, indentOff int) {
	pad := strings.Repeat("  ", indentOff)
	fmt.Fprintf(b, "%smcp:\n", pad)
	doc := ""
	if k, ok := KeyByPath("mcp.max_inline_tools"); ok {
		doc = k.Doc
	}
	fmt.Fprintf(b, "%s  max_inline_tools: %d   # %s\n", pad, c.MCP.MaxInlineTools, doc)
	names := c.MCPServerNames()
	if len(names) == 0 {
		fmt.Fprintf(b, "%s  servers: {}\n", pad)
		return
	}
	fmt.Fprintf(b, "%s  servers:\n", pad)
	for _, name := range names {
		fmt.Fprintf(b, "%s    %s:\n", pad, yamlScalar(name))
		writeMCPServer(b, c.MCP.Servers[name], mask, indentOff+3)
	}
}

func writeMCPServer(b *strings.Builder, s MCPServer, mask bool, indent int) {
	pad := strings.Repeat("  ", indent)
	fmt.Fprintf(b, "%stransport: %s\n", pad, yamlScalar(s.Transport))
	if s.Command != "" {
		fmt.Fprintf(b, "%scommand: %s\n", pad, yamlScalar(s.Command))
	}
	if len(s.Args) > 0 {
		fmt.Fprintf(b, "%sargs: %s\n", pad, flowList(s.Args))
	}
	if s.URL != "" {
		fmt.Fprintf(b, "%surl: %s\n", pad, yamlScalar(s.URL))
	}
	if len(s.Env) > 0 {
		fmt.Fprintf(b, "%senv:\n", pad)
		for _, key := range sortedKeys(s.Env) {
			fmt.Fprintf(b, "%s  %s: %s\n", pad, key, yamlScalar(mcpMaskedValue(s.Env[key], mask)))
		}
	}
	if len(s.Headers) > 0 {
		fmt.Fprintf(b, "%sheaders:\n", pad)
		for _, key := range sortedKeys(s.Headers) {
			fmt.Fprintf(b, "%s  %s: %s\n", pad, yamlScalar(key), yamlScalar(mcpMaskedValue(s.Headers[key], mask)))
		}
	}
	fmt.Fprintf(b, "%senabled: %t\n", pad, s.Enabled)
	fmt.Fprintf(b, "%strust: %s\n", pad, yamlScalar(s.TrustMode()))
	fmt.Fprintf(b, "%stimeout_ms: %d\n", pad, s.Timeout())
	fmt.Fprintf(b, "%sprivate: %t\n", pad, s.Private)
	if len(s.Tools.Allow) == 0 && len(s.Tools.Deny) == 0 && len(s.Tools.ReadOnly) == 0 {
		return
	}
	fmt.Fprintf(b, "%stools:\n", pad)
	for _, list := range []struct {
		field string
		globs []string
	}{
		{"allow", s.Tools.Allow},
		{"deny", s.Tools.Deny},
		{"read_only", s.Tools.ReadOnly},
	} {
		if len(list.globs) > 0 {
			fmt.Fprintf(b, "%s  %s: %s\n", pad, list.field, flowList(list.globs))
		}
	}
}

// mcpMaskedValue hides a literal value but keeps a pure ${ENV_VAR} reference.
func mcpMaskedValue(value string, mask bool) string {
	if !mask {
		return value
	}
	trimmed := strings.TrimSpace(value)
	if mcpEnvReference.FindString(trimmed) == trimmed && trimmed != "" {
		return trimmed
	}
	return "***"
}

// writeMCPDiff lists server settings that differ from the built-in defaults.
// Env and header values are masked on both sides; the source is always the
// file because MCP settings have no environment override.
func writeMCPDiff(b *strings.Builder, base, fileCfg, full Config) {
	names := map[string]bool{}
	for _, name := range base.MCPServerNames() {
		names[name] = true
	}
	for _, name := range full.MCPServerNames() {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		bs, _ := base.MCPServer(name)
		fs, _ := fileCfg.MCPServer(name)
		es, _ := full.MCPServer(name)
		fields := []struct {
			field string
			show  func(MCPServer) string
			mask  bool
		}{
			{"transport", func(s MCPServer) string { return s.Transport }, false},
			{"command", func(s MCPServer) string { return s.Command }, false},
			{"args", func(s MCPServer) string { return strings.Join(s.Args, " ") }, false},
			{"url", func(s MCPServer) string { return s.URL }, false},
			{"enabled", func(s MCPServer) string { return fmt.Sprintf("%t", s.Enabled) }, false},
			{"trust", func(s MCPServer) string { return s.TrustMode() }, false},
			{"timeout_ms", func(s MCPServer) string { return fmt.Sprintf("%d", s.Timeout()) }, false},
			{"private", func(s MCPServer) string { return fmt.Sprintf("%t", s.Private) }, false},
			{"tools.allow", func(s MCPServer) string { return flowList(s.Tools.Allow) }, false},
			{"tools.deny", func(s MCPServer) string { return flowList(s.Tools.Deny) }, false},
			{"tools.read_only", func(s MCPServer) string { return flowList(s.Tools.ReadOnly) }, false},
		}
		for _, field := range fields {
			dv, fv, ev := "", "", ""
			if _, ok := base.MCPServer(name); ok {
				dv = field.show(bs)
			}
			if _, ok := fileCfg.MCPServer(name); ok {
				fv = field.show(fs)
			}
			if _, ok := full.MCPServer(name); ok {
				ev = field.show(es)
			}
			if ev == dv {
				continue
			}
			source := "file"
			if ev != fv {
				source = "env"
			}
			fmt.Fprintf(b, "mcp.servers.%s.%s  %s -> %s  (%s)\n", name, field.field, mcpDiffValue(dv, field.mask), mcpDiffValue(ev, field.mask), source)
		}
		writeMCPValueDiff(b, base, fileCfg, full, name, "env", func(s MCPServer) map[string]string { return s.Env })
		writeMCPValueDiff(b, base, fileCfg, full, name, "headers", func(s MCPServer) map[string]string { return s.Headers })
	}
}

func writeMCPValueDiff(b *strings.Builder, base, fileCfg, full Config, name, section string, get func(MCPServer) map[string]string) {
	keys := map[string]bool{}
	for _, c := range []Config{base, fileCfg, full} {
		if s, ok := c.MCPServer(name); ok {
			for key := range get(s) {
				keys[key] = true
			}
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		dv, fv, ev := mcpMapValue(base, name, section, get, key), mcpMapValue(fileCfg, name, section, get, key), mcpMapValue(full, name, section, get, key)
		if dv == ev {
			continue
		}
		source := "file"
		if fv != ev {
			source = "env"
		}
		fmt.Fprintf(b, "mcp.servers.%s.%s.%s  %s -> %s  (%s)\n", name, section, key, mcpDiffValue(dv, true), mcpDiffValue(ev, true), source)
	}
}

func mcpMapValue(c Config, name, section string, get func(MCPServer) map[string]string, key string) string {
	s, ok := c.MCPServer(name)
	if !ok {
		return ""
	}
	return get(s)[key]
}

// mcpDiffValue renders one diff side: empty text is an absent setting and a
// masked value is hidden entirely.
func mcpDiffValue(value string, mask bool) string {
	if value == "" {
		return "<absent>"
	}
	if mask {
		return "***"
	}
	return yamlScalar(value)
}
