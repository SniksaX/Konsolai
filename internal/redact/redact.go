package redact

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

type rule struct {
	name string
	re   *regexp.Regexp
	repl string
}

var rules []rule

func init() {
	rules = []rule{
		{
			name: "pem",
			re:   regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
			repl: "[REDACTED:pem]",
		},
		{
			name: "aws",
			re:   regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`),
			repl: "[REDACTED:aws]",
		},
		{
			name: "github",
			re:   regexp.MustCompile(`(?:ghp|gho|ghu|ghs)_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}`),
			repl: "[REDACTED:github]",
		},
		{
			name: "slack",
			re:   regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`),
			repl: "[REDACTED:slack]",
		},
		{
			name: "sk",
			re:   regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
			repl: "[REDACTED:sk]",
		},
		{
			name: "jwt",
			re:   regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
			repl: "[REDACTED:jwt]",
		},
		{
			name: "bearer",
			re:   regexp.MustCompile(`(?i)Bearer [A-Za-z0-9._\-+/=]+`),
			repl: "[REDACTED:bearer]",
		},
		{
			name: "urlpass",
			re:   regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/?#\s]+):([^@/\s]+)@`),
			repl: "${1}:[REDACTED:urlpass]@",
		},
		{
			name: "kv",
			re:   regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|passw(?:or)?d|auth(?:orization)?|private[_-]?key)(?:\s*=\s*|\s*:\s+))(\S+)`),
			repl: "${1}[REDACTED:kv]",
		},
	}
}

func String(s string) string {
	for _, r := range rules {
		if r.name == "kv" {
			s = r.re.ReplaceAllStringFunc(s, func(m string) string {
				sub := r.re.FindStringSubmatch(m)
				if len(sub) == 3 && strings.HasPrefix(sub[2], "[REDACTED:") {
					return m
				}
				return r.re.ReplaceAllString(m, r.repl)
			})
			continue
		}
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

func JSON(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return raw
	}
	switch raw[0] {
	case '{':
		return redactObject(raw)
	case '[':
		return redactArray(raw)
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return raw
		}
		out, err := json.Marshal(String(s))
		if err != nil {
			return raw
		}
		return out
	default:
		return raw
	}
}

func redactObject(raw []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	for k, v := range obj {
		obj[k] = json.RawMessage(JSON(v))
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return b
}

func redactArray(raw []byte) []byte {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return raw
	}
	for i, v := range arr {
		arr[i] = json.RawMessage(JSON(v))
	}
	b, err := json.Marshal(arr)
	if err != nil {
		return raw
	}
	return b
}

func SensitivePath(path string) bool {
	base := filepath.Base(path)
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".key"), strings.HasSuffix(base, ".p12"):
		return true
	case strings.HasPrefix(base, "id_rsa"), strings.HasPrefix(base, "id_ed25519"):
		return true
	case base == ".netrc", base == ".pgpass":
		return true
	case strings.HasPrefix(base, "credentials") && strings.HasSuffix(base, ".json"):
		return true
	default:
		return false
	}
}
