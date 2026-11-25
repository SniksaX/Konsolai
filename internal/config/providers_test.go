package config

import (
	"net/url"
	"testing"
)

func TestProviderPresets(t *testing.T) {
	names := ProviderNames()
	if len(names) != 8 {
		t.Fatalf("providers %v", names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		p, ok := Provider(name)
		if !ok || p.Endpoint == "" || p.APIKeyEnv == "" || p.Context <= 0 {
			t.Fatalf("%s: %+v", name, p)
		}
		u, err := url.Parse(p.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			t.Fatalf("%s endpoint %q", name, p.Endpoint)
		}
		if p.MaxTokensField != "max_tokens" && p.MaxTokensField != "max_completion_tokens" {
			t.Fatalf("%s field %q", name, p.MaxTokensField)
		}
		if seen[name] {
			t.Fatalf("duplicate %s", name)
		}
		seen[name] = true
	}
	if _, ok := Provider("nope"); ok {
		t.Fatal("unknown provider accepted")
	}

	var m Model
	p, _ := Provider("openai")
	ApplyProvider(&m, p)
	if m.Endpoint != p.Endpoint || m.APIKeyEnv != p.APIKeyEnv || m.Context != p.Context || m.MaxTokensField != "max_completion_tokens" || m.SendsTemperature() {
		t.Fatalf("applied %+v", m)
	}
}
