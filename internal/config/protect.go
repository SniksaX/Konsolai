package config

import (
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ProtectedKind classifies a path that the model must not read raw or write.
const (
	ProtectedConfig   = "config"
	ProtectedBackup   = "backup"
	ProtectedSecrets  = "secrets"
	ProtectedRefusal  = "the configuration is changed only through /set; view it with /config show"
	ProtectedReadHint = "the configuration is masked for the model; use /config show to inspect it"
)

// ActiveConfigPath is the config file the process is using (KONSOLAI_CONFIG or
// the XDG default; a --config flag is expected to set KONSOLAI_CONFIG).
func ActiveConfigPath() string {
	return ResolveConfigPath("")
}

// realOrSelf follows symlinks when the target exists and otherwise resolves the
// parent, so a new file beside the config still compares as protected.
func realOrSelf(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(real)
	}
	dir, base := filepath.Split(p)
	if realDir, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(realDir, base)
	}
	return p
}

// ProtectedPaths lists the active config, its backups and the secrets file.
func ProtectedPaths() []string {
	active := realOrSelf(ActiveConfigPath())
	out := []string{active}
	if matches, err := filepath.Glob(active + ".bak-*"); err == nil {
		for _, m := range matches {
			out = append(out, realOrSelf(m))
		}
	}
	if matches, err := filepath.Glob(ActiveConfigPath() + ".bak-*"); err == nil {
		for _, m := range matches {
			out = append(out, realOrSelf(m))
		}
	}
	if secrets, err := SecretPath(); err == nil {
		out = append(out, realOrSelf(secrets))
	}
	return out
}

// ProtectedKindOf reports whether path is the active config, a backup or the
// secrets file. The comparison follows symlinks.
func ProtectedKindOf(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	p := realOrSelf(path)
	active := realOrSelf(ActiveConfigPath())
	switch {
	case p == active:
		return ProtectedConfig, true
	case strings.HasPrefix(p, active+".bak-"):
		return ProtectedBackup, true
	}
	dir := filepath.Dir(active)
	base := filepath.Base(p)
	if filepath.Dir(p) == dir {
		if base == "config.yaml" || strings.HasPrefix(base, "config.yaml.bak-") {
			if base == "config.yaml" {
				return ProtectedConfig, true
			}
			return ProtectedBackup, true
		}
	}
	if secrets, err := SecretPath(); err == nil && p == realOrSelf(secrets) {
		return ProtectedSecrets, true
	}
	return "", false
}

// IsProtectedWritePath reports a path the file tools may never write.
func IsProtectedWritePath(path string) bool {
	_, ok := ProtectedKindOf(path)
	return ok
}

// ShellProtected reports a protected name mentioned by a shell command. Any
// mention is refused: a denied shell command is safer than a leaky reader and
// the config is changed only through /set.
func ShellProtected(cmd string) (string, bool) {
	lower := strings.ToLower(cmd)
	for _, name := range []string{"config.yaml", ".config/konsolai/secrets", "konsolai_config"} {
		if strings.Contains(lower, name) {
			return name, true
		}
	}
	if secrets, err := SecretPath(); err == nil {
		if strings.Contains(lower, strings.ToLower(filepath.Base(secrets))) && strings.Contains(lower, "konsolai") {
			return "secrets", true
		}
	}
	return "", false
}

// MaskConfigContent renders a config file with the secrets and private
// endpoints hidden: MCP command/args/env/headers, MCP and database hostnames.
// Unknown or non-YAML content returns a refusal marker, never the raw text.
func MaskConfigContent(raw string) string {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil || doc == nil {
		return "[config content masked: " + ProtectedReadHint + "]\n"
	}
	maskMCP(doc)
	maskDatabase(doc)
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "[config content masked: " + ProtectedReadHint + "]\n"
	}
	return string(out)
}

func configRoot(doc map[string]any) map[string]any {
	if d, ok := doc["defaults"].(map[string]any); ok {
		return d
	}
	return doc
}

func maskMCP(doc map[string]any) {
	mcp, _ := configRoot(doc)["mcp"].(map[string]any)
	if mcp == nil {
		return
	}
	servers, _ := mcp["servers"].(map[string]any)
	for _, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		server["command"] = "<set>"
		if _, ok := server["args"]; ok {
			server["args"] = []any{"<set>"}
		}
		if env, ok := server["env"].(map[string]any); ok {
			for k := range env {
				env[k] = "<set>"
			}
		}
		if headers, ok := server["headers"].(map[string]any); ok {
			for k := range headers {
				headers[k] = "<set>"
			}
		}
		if url, ok := server["url"].(string); ok {
			server["url"] = maskURLHost(url)
		}
	}
}

func maskDatabase(doc map[string]any) {
	db, _ := configRoot(doc)["database"].(map[string]any)
	if db == nil {
		return
	}
	if url, ok := db["url"].(string); ok {
		db["url"] = maskURLHost(url)
	}
}

// maskURLHost keeps the scheme and path shape but hides the host.
func maskURLHost(raw string) string {
	if raw == "" {
		return raw
	}
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return "<masked>"
	}
	rest := raw[schemeEnd+3:]
	end := len(rest)
	for i, r := range rest {
		if r == '/' || r == '?' || r == '#' {
			end = i
			break
		}
	}
	return raw[:schemeEnd+3] + "<masked>" + rest[end:]
}
