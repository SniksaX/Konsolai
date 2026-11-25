package config

import "strings"

// ProviderPreset is the endpoint and request quirks for one API-key provider.
// The endpoints come from each provider's OpenAI-compatible docs and are not
// exercised against the live services here.
type ProviderPreset struct {
	Name            string
	Endpoint        string
	APIKeyEnv       string
	Context         int
	MaxTokensField  string
	SendTemperature bool
	Note            string
}

var providerPresets = []ProviderPreset{
	{Name: "openai", Endpoint: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", Context: 128000, MaxTokensField: "max_completion_tokens", SendTemperature: false},
	{Name: "openrouter", Endpoint: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY", Context: 128000, MaxTokensField: "max_tokens", SendTemperature: true},
	{Name: "groq", Endpoint: "https://api.groq.com/openai/v1", APIKeyEnv: "GROQ_API_KEY", Context: 131072, MaxTokensField: "max_tokens", SendTemperature: true},
	{Name: "together", Endpoint: "https://api.together.xyz/v1", APIKeyEnv: "TOGETHER_API_KEY", Context: 131072, MaxTokensField: "max_tokens", SendTemperature: true},
	{Name: "mistral", Endpoint: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY", Context: 128000, MaxTokensField: "max_tokens", SendTemperature: true},
	{Name: "deepseek", Endpoint: "https://api.deepseek.com/v1", APIKeyEnv: "DEEPSEEK_API_KEY", Context: 128000, MaxTokensField: "max_tokens", SendTemperature: false, Note: "the reasoner may reject temperature"},
	{Name: "gemini", Endpoint: "https://generativelanguage.googleapis.com/v1beta/openai", APIKeyEnv: "GEMINI_API_KEY", Context: 1048576, MaxTokensField: "max_tokens", SendTemperature: true},
	{Name: "anthropic", Endpoint: "https://api.anthropic.com/v1", APIKeyEnv: "ANTHROPIC_API_KEY", Context: 200000, MaxTokensField: "max_tokens", SendTemperature: true, Note: "OpenAI-compatible endpoint; auth header not verified"},
}

// ProviderNames lists the presets in table order.
func ProviderNames() []string {
	out := make([]string, 0, len(providerPresets))
	for _, p := range providerPresets {
		out = append(out, p.Name)
	}
	return out
}

// Provider looks up one provider by name.
func Provider(name string) (ProviderPreset, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range providerPresets {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderPreset{}, false
}

// ApplyProvider folds a preset into a model entry.
func ApplyProvider(m *Model, p ProviderPreset) {
	if m == nil {
		return
	}
	m.Endpoint = p.Endpoint
	m.APIKeyEnv = p.APIKeyEnv
	m.Context = p.Context
	m.MaxTokensField = p.MaxTokensField
	send := p.SendTemperature
	m.SendTemperature = &send
}
