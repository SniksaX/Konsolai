package redact

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
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
			re:   regexp.MustCompile(`(?i)((?:[A-Za-z0-9_.-]*(?:KEYS?|TOKENS?|SECRETS?|PASSWORDS?|PASSWDS?|PASS|CREDENTIALS?|PRIVATE)[A-Za-z0-9_.-]*|auth(?:orization)?)(?:\s*=\s*|\s*:\s+))(\S+)`),
			repl: "${1}[REDACTED:kv]",
		},
		{
			name: "env",
			re:   regexp.MustCompile(`(?m)^(\s*[A-Za-z_][A-Za-z0-9_]*\s*[=:]\s*)([A-Fa-f0-9]{32,}|[A-Za-z0-9_-]{32,})\s*$`),
			repl: "${1}[REDACTED:env]",
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

var envAssign = regexp.MustCompile(`^(\s*(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*\s*[=:]\s*)(\S+)(.*)$`)

func MaskValues(s string) (string, int) {
	if s == "" {
		return s, 0
	}
	lines := strings.Split(s, "\n")
	n := 0
	for i, line := range lines {
		sub := envAssign.FindStringSubmatch(line)
		if sub == nil || sub[2] == "" {
			continue
		}
		n++
		lines[i] = sub[1] + maskKeep4(sub[2]) + sub[3]
	}
	return strings.Join(lines, "\n"), n
}

func maskKeep4(v string) string {
	keep := v
	if len(v) > 4 {
		keep = v[:4]
	}
	return keep + "…[masked " + strconv.Itoa(len(v)) + " chars]"
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
