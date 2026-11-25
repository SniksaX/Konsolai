package config

import (
	"net/url"
	"testing"
)

// presetCase is the published endpoint, key variable and context for one
// provider preset.
type presetCase struct {
	name    string
	host    string
	keyEnv  string
	context int
}

var presetCases = []presetCase{
	{name: "openai", host: "api.openai.com", keyEnv: "OPENAI_API_KEY", context: 128000},
	{name: "openrouter", host: "openrouter.ai", keyEnv: "OPENROUTER_API_KEY", context: 128000},
	{name: "groq", host: "api.groq.com", keyEnv: "GROQ_API_KEY", context: 131072},
	{name: "together", host: "api.together.xyz", keyEnv: "TOGETHER_API_KEY", context: 131072},
	{name: "mistral", host: "api.mistral.ai", keyEnv: "MISTRAL_API_KEY", context: 128000},
	{name: "deepseek", host: "api.deepseek.com", keyEnv: "DEEPSEEK_API_KEY", context: 128000},
	{name: "gemini", host: "generativelanguage.googleapis.com", keyEnv: "GEMINI_API_KEY", context: 1048576},
	{name: "anthropic", host: "api.anthropic.com", keyEnv: "ANTHROPIC_API_KEY", context: 200000},
}

func TestProviderPresetFields(t *testing.T) {
	if len(presetCases) != len(ProviderNames()) {
		t.Fatalf("preset cases %d, providers %d", len(presetCases), len(ProviderNames()))
	}
	for _, tc := range presetCases {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := Provider(tc.name)
			if !ok {
				t.Fatalf("provider %q is missing", tc.name)
			}
			u, err := url.Parse(p.Endpoint)
			if err != nil || u.Scheme != "https" || u.Host != tc.host {
				t.Fatalf("endpoint %q, want https://%s", p.Endpoint, tc.host)
			}
			if p.APIKeyEnv != tc.keyEnv {
				t.Fatalf("key env %q, want %q", p.APIKeyEnv, tc.keyEnv)
			}
			if p.Context != tc.context {
				t.Fatalf("context %d, want %d", p.Context, tc.context)
			}
			if p.MaxTokensField != "max_tokens" && p.MaxTokensField != "max_completion_tokens" {
				t.Fatalf("max_tokens_field %q", p.MaxTokensField)
			}

			var m Model
			ApplyProvider(&m, p)
			if m.Endpoint != p.Endpoint || m.APIKeyEnv != tc.keyEnv || m.Context != tc.context {
				t.Fatalf("applied model %+v", m)
			}
			if m.MaxTokensField != p.MaxTokensField || m.SendsTemperature() != p.SendTemperature {
				t.Fatalf("applied request quirks %+v", m)
			}
		})
	}
}

func TestProviderPresetAnthropicNote(t *testing.T) {
	p, ok := Provider("anthropic")
	if !ok {
		t.Fatal("the anthropic preset is missing")
	}
	if p.Note == "" {
		t.Fatal("the anthropic preset has no experimental note")
	}
	for _, other := range ProviderNames() {
		if other == "anthropic" {
			continue
		}
		o, _ := Provider(other)
		if o.Note != "" {
			t.Fatalf("provider %q unexpectedly carries a note: %q", other, o.Note)
		}
	}
}
