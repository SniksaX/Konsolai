package mcp

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// envRef matches a ${NAME} reference inside a declared environment value or
// request header. The braced form is the only one expanded, so a literal $
// stays literal.
var envRef = &envScanner{}

type envScanner struct{}

// expand replaces ${NAME} with the process environment. The first unset or
// empty variable aborts with an error that names the variable and never its
// value.
func (envScanner) expand(raw string) (string, error) {
	var missing string
	var b strings.Builder
	for i := 0; i < len(raw); {
		if raw[i] == '$' && i+1 < len(raw) && raw[i+1] == '{' {
			end := strings.IndexByte(raw[i+2:], '}')
			if end >= 0 {
				name := raw[i+2 : i+2+end]
				if name != "" {
					value, ok := os.LookupEnv(name)
					if !ok || value == "" {
						if missing == "" {
							missing = name
						}
					} else {
						b.WriteString(value)
					}
					i += 2 + end + 1
					continue
				}
			}
		}
		b.WriteByte(raw[i])
		i++
	}
	if missing != "" {
		return "", fmt.Errorf("environment variable %q is not set", missing)
	}
	return b.String(), nil
}

// resolveValue resolves one declared value.
func resolveValue(raw string) (string, error) {
	return envRef.expand(raw)
}

// childEnv is the exact environment of a stdio child: PATH and HOME from the
// process, plus every declared entry. Nothing else is inherited, so an
// ambient secret cannot reach the server.
func childEnv(declared map[string]string) ([]string, error) {
	values := make(map[string]string, len(declared)+2)
	values["PATH"] = os.Getenv("PATH")
	values["HOME"] = os.Getenv("HOME")
	for name, raw := range declared {
		value, err := resolveValue(raw)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", name, err)
		}
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return out, nil
}
