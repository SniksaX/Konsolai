package config

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// MCP transports, trust levels and the default per-server timeout.
const (
	MCPTransportStdio = "stdio"
	MCPTransportHTTP  = "http"

	MCPTrustAsk      = "ask"
	MCPTrustReadOnly = "read_only"
	MCPTrustAuto     = "auto"

	MCPDefaultTimeoutMS = 30000
)

// MCP is the MCP server section. The server map is dynamic: its entries are
// not keys, so they are applied by applyMCPServerMap instead of the key table.
type MCP struct {
	MaxInlineTools int                  `yaml:"max_inline_tools"`
	Servers        map[string]MCPServer `yaml:"servers"`
}

// MCPServer is one configured MCP server.
type MCPServer struct {
	Transport string            `yaml:"transport"`
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	Enabled   bool              `yaml:"enabled"`
	Trust     string            `yaml:"trust"`
	Tools     MCPTools          `yaml:"tools"`
	TimeoutMS int               `yaml:"timeout_ms"`
	Private   bool              `yaml:"private"`
}

// MCPTools is the per-server tool filter.
type MCPTools struct {
	Allow    []string `yaml:"allow"`
	Deny     []string `yaml:"deny"`
	ReadOnly []string `yaml:"read_only"`
}

// DefaultMCPServer returns the values a newly declared server starts from.
func DefaultMCPServer() MCPServer {
	return MCPServer{Enabled: true, Trust: MCPTrustAsk, TimeoutMS: MCPDefaultTimeoutMS}
}

// TrustMode is the normalized trust level; an empty value means ask.
func (s MCPServer) TrustMode() string {
	switch t := strings.ToLower(strings.TrimSpace(s.Trust)); t {
	case "":
		return MCPTrustAsk
	default:
		return t
	}
}

// Timeout is the per-server timeout; a missing value means the default.
func (s MCPServer) Timeout() int {
	if s.TimeoutMS <= 0 {
		return MCPDefaultTimeoutMS
	}
	return s.TimeoutMS
}

// MCPServer returns one configured server.
func (c Config) MCPServer(name string) (MCPServer, bool) {
	s, ok := c.MCP.Servers[strings.TrimSpace(name)]
	return s, ok
}

// MCPServerNames lists configured server names in sorted order.
func (c Config) MCPServerNames() []string {
	names := make([]string, 0, len(c.MCP.Servers))
	for name := range c.MCP.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MCPInlineCap is the inline tool cap; a negative value clamps to zero.
func (c Config) MCPInlineCap() int {
	if c.MCP.MaxInlineTools < 0 {
		return 0
	}
	return c.MCP.MaxInlineTools
}

// ValidMCPServerName reports whether name can be a servers map key.
func ValidMCPServerName(name string) bool {
	if name == "" || strings.ContainsAny(name, " \t\n.:/\\=") {
		return false
	}
	return true
}

// applyMCPServerMap applies one defaults.mcp.servers or profile node. Names
// merge field by field over the values already applied, like models.
func applyMCPServerMap(st *walkState, n *yaml.Node, full string) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyMCPServerMap(st, n.Alias, full)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s must be a mapping (line %d)", full, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		name := kn.Value
		if !ValidMCPServerName(name) {
			return fmt.Errorf("config: %s.%s is not a valid server name (line %d)", full, name, kn.Line)
		}
		server := DefaultMCPServer()
		if st != nil && st.apply && st.c != nil {
			if current, ok := st.c.MCP.Servers[name]; ok {
				server = current
			}
		}
		if err := applyMCPServerFields(&server, vn, full+"."+name); err != nil {
			return err
		}
		if st != nil && st.apply && st.c != nil {
			if st.c.MCP.Servers == nil {
				st.c.MCP.Servers = map[string]MCPServer{}
			}
			st.c.MCP.Servers[name] = server
			noteSrc(st, "mcp.servers."+name)
		}
	}
	return nil
}

// applyMCPServerFields applies one server mapping and rejects unknown fields.
func applyMCPServerFields(s *MCPServer, n *yaml.Node, full string) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyMCPServerFields(s, n.Alias, full)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s must be a mapping (line %d)", full, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		name := kn.Value
		child := full + "." + name
		if isNull(vn) {
			continue
		}
		switch name {
		case "transport":
			v, err := mcpString(vn, child)
			if err != nil {
				return err
			}
			s.Transport = v
		case "command":
			v, err := mcpString(vn, child)
			if err != nil {
				return err
			}
			s.Command = v
		case "args":
			v, err := parseList(vn, child)
			if err != nil {
				return err
			}
			s.Args = v
		case "env":
			v, err := mcpStringMap(vn, child)
			if err != nil {
				return err
			}
			s.Env = v
		case "url":
			v, err := mcpString(vn, child)
			if err != nil {
				return err
			}
			s.URL = v
		case "headers":
			v, err := mcpStringMap(vn, child)
			if err != nil {
				return err
			}
			s.Headers = v
		case "enabled":
			b, err := parseBool(vn, child)
			if err != nil {
				return err
			}
			s.Enabled = b
		case "trust":
			v, err := mcpString(vn, child)
			if err != nil {
				return err
			}
			s.Trust = v
		case "tools":
			if err := applyMCPToolsFields(&s.Tools, vn, child); err != nil {
				return err
			}
		case "timeout_ms":
			v, err := parseInt(vn, child)
			if err != nil {
				return err
			}
			s.TimeoutMS = v
		case "private":
			b, err := parseBool(vn, child)
			if err != nil {
				return err
			}
			s.Private = b
		default:
			return fmt.Errorf("config: unknown key %q at line %d", child, kn.Line)
		}
	}
	return nil
}

func applyMCPToolsFields(t *MCPTools, n *yaml.Node, full string) error {
	if n == nil || isNull(n) {
		return nil
	}
	if n.Kind == yaml.AliasNode {
		return applyMCPToolsFields(t, n.Alias, full)
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s must be a mapping (line %d)", full, n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		child := full + "." + kn.Value
		if isNull(vn) {
			continue
		}
		v, err := parseList(vn, child)
		if err != nil {
			return err
		}
		switch kn.Value {
		case "allow":
			t.Allow = v
		case "deny":
			t.Deny = v
		case "read_only":
			t.ReadOnly = v
		default:
			return fmt.Errorf("config: unknown key %q at line %d", child, kn.Line)
		}
	}
	return nil
}

func mcpString(n *yaml.Node, full string) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("config: %s must be a string (line %d)", full, n.Line)
	}
	return n.Value, nil
}

func mcpStringMap(n *yaml.Node, full string) (map[string]string, error) {
	if n.Kind == yaml.AliasNode {
		return mcpStringMap(n.Alias, full)
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config: %s must be a mapping (line %d)", full, n.Line)
	}
	out := make(map[string]string, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		kn := n.Content[i]
		vn := n.Content[i+1]
		if vn.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("config: %s.%s must be a string (line %d)", full, kn.Value, vn.Line)
		}
		out[kn.Value] = vn.Value
	}
	return out, nil
}

// validateMCP checks every configured server.
func validateMCP(c Config) error {
	for _, name := range c.MCPServerNames() {
		if err := validateMCPServer(name, c.MCP.Servers[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateMCPServer(name string, s MCPServer) error {
	base := "mcp.servers." + name
	switch transport := strings.ToLower(strings.TrimSpace(s.Transport)); transport {
	case MCPTransportStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("config: %s.command is required for the stdio transport", base)
		}
		if strings.TrimSpace(s.URL) != "" {
			return fmt.Errorf("config: %s.url must be empty for the stdio transport", base)
		}
	case MCPTransportHTTP:
		if strings.TrimSpace(s.Command) != "" {
			return fmt.Errorf("config: %s.command must be empty for the http transport", base)
		}
		if err := validateMCPURL(base, s.URL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("config: %s.transport must be stdio or http, got %q", base, s.Transport)
	}
	switch s.TrustMode() {
	case MCPTrustAsk, MCPTrustReadOnly, MCPTrustAuto:
	default:
		return fmt.Errorf("config: %s.trust must be ask, read_only or auto, got %q", base, s.Trust)
	}
	if s.TimeoutMS <= 0 {
		return fmt.Errorf("config: %s.timeout_ms must be a positive integer, got %d", base, s.TimeoutMS)
	}
	for _, list := range []struct {
		field string
		globs []string
	}{
		{"tools.allow", s.Tools.Allow},
		{"tools.deny", s.Tools.Deny},
		{"tools.read_only", s.Tools.ReadOnly},
	} {
		for _, glob := range list.globs {
			if err := ValidateToolGlob(glob); err != nil {
				return fmt.Errorf("config: %s.%s: %w", base, list.field, err)
			}
		}
	}
	for _, key := range sortedKeys(s.Env) {
		if err := ValidateEnvName(key); err != nil {
			return fmt.Errorf("config: %s.env: %w", base, err)
		}
		if LiteralSecret(s.Env[key]) {
			return fmt.Errorf("config: %s.env.%s: a literal secret is not allowed; use ${ENV_VAR} or a non-secret value", base, key)
		}
	}
	for _, key := range sortedKeys(s.Headers) {
		if err := ValidateHeaderName(key); err != nil {
			return fmt.Errorf("config: %s.headers: %w", base, err)
		}
		if LiteralSecret(s.Headers[key]) {
			return fmt.Errorf("config: %s.headers.%s: a literal secret is not allowed; use ${ENV_VAR} or a non-secret value", base, key)
		}
	}
	return nil
}

func validateMCPURL(base, raw string) error {
	u, err := parseHTTPURL(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("config: %s.url is not an HTTP URL: %q", base, raw)
	}
	if strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	if HostRemote(u.Hostname()) {
		return fmt.Errorf("config: %s.url must use https for a remote host; plain http is only allowed on a loopback or private host", base)
	}
	return nil
}

var (
	mcpEnvName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	mcpHeaderName   = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
	mcpEnvReference = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	mcpSecretRes    = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`(?:ghp|gho|ghu|ghs)_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token)\s*[=:]\s*[A-Za-z0-9]`),
	}
	mcpLongToken = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)
)

// ValidateEnvName checks an environment variable name in an env map.
func ValidateEnvName(name string) error {
	if !mcpEnvName.MatchString(name) {
		return fmt.Errorf("invalid environment variable name %q", name)
	}
	return nil
}

// ValidateHeaderName checks an HTTP header name in a headers map.
func ValidateHeaderName(name string) error {
	if !mcpHeaderName.MatchString(name) {
		return fmt.Errorf("invalid header name %q", name)
	}
	return nil
}

// ValidateToolGlob checks one MCP tool glob. A glob is a shell pattern over a
// tool name, for example "read_*".
func ValidateToolGlob(glob string) error {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return fmt.Errorf("a tool glob must not be empty")
	}
	if strings.ContainsAny(glob, " \t\n") {
		return fmt.Errorf("a tool glob must not contain whitespace: %q", glob)
	}
	if _, err := path.Match(glob, "tool"); err != nil {
		return fmt.Errorf("invalid tool glob %q: %w", glob, err)
	}
	return nil
}

// LiteralSecret reports whether a value looks like a stored credential rather
// than a ${ENV_VAR} reference. References are stripped before the check so
// "Bearer ${TOKEN}" stays allowed while "Bearer abc123..." does not.
func LiteralSecret(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	stripped := mcpEnvReference.ReplaceAllString(value, " ")
	for _, re := range mcpSecretRes {
		if re.MatchString(stripped) {
			return true
		}
	}
	for _, token := range mcpLongToken.FindAllString(stripped, -1) {
		letters, digits := false, false
		for _, r := range token {
			if unicode.IsLetter(r) {
				letters = true
			}
			if unicode.IsDigit(r) {
				digits = true
			}
		}
		if letters && digits {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
