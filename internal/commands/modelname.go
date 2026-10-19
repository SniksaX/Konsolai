package commands

import (
	"regexp"
	"strings"
)

// quantSuffix matches a trailing quantization or container token.
var quantSuffix = regexp.MustCompile(`(?i)^(q\d+(_[a-z0-9]+)*|iq\d+(_[a-z0-9]+)*|nvfp4|awq|gptq|gguf|ggml|fp16|fp32|fp8|bf16|int4|int8|exl2)$`)

var nameExt = map[string]bool{
	"gguf": true, "bin": true, "safetensors": true, "ggml": true, "pt": true,
}

// SuggestModelName derives a short config name from a server model id.
// It keeps the basename, drops a container extension and quantization suffix,
// lowercases, turns dots into dashes, and stops at 24 characters.
func SuggestModelName(id string) string {
	id = strings.TrimSpace(strings.ReplaceAll(id, "\\", "/"))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if dot := strings.LastIndex(id, "."); dot > 0 {
		ext := strings.ToLower(id[dot+1:])
		if nameExt[ext] {
			id = id[:dot]
		}
	}
	parts := splitModelName(id)
	for len(parts) > 0 && quantSuffix.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	s := strings.ToLower(strings.Join(parts, "-"))
	s = strings.Trim(s, "-")
	if s == "" {
		s = "model"
	}
	if len(s) > 24 {
		s = strings.Trim(s[:24], "-")
		if s == "" {
			s = "model"
		}
	}
	return s
}

func splitModelName(s string) []string {
	var parts []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		parts = append(parts, b.String())
		b.Reset()
	}
	for _, r := range s {
		switch r {
		case '-', '.', ' ':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return parts
}
