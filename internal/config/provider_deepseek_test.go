package config

import (
	"strings"
	"testing"
)

func TestDeepSeekPreset(t *testing.T) {
	p, ok := Provider("deepseek")
	if !ok {
		t.Fatal("the deepseek preset is missing")
	}
	if p.Endpoint != "https://api.deepseek.com/v1" {
		t.Fatalf("endpoint %q", p.Endpoint)
	}
	if p.APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("api_key_env %q", p.APIKeyEnv)
	}
	if p.Context != 128000 {
		t.Fatalf("context %d", p.Context)
	}
	if p.MaxTokensField != "max_tokens" {
		t.Fatalf("max_tokens_field %q", p.MaxTokensField)
	}
	if p.SendTemperature {
		t.Fatal("the deepseek preset must not send temperature")
	}
	if !strings.Contains(p.Note, "reasoner") || !strings.Contains(p.Note, "temperature") {
		t.Fatalf("note %q does not warn about the reasoner temperature", p.Note)
	}

	var m Model
	ApplyProvider(&m, p)
	if m.SendTemperature == nil || *m.SendTemperature || m.SendsTemperature() {
		t.Fatalf("applied send_temperature %+v", m.SendTemperature)
	}
	if m.SendTemperature == nil {
		t.Fatal("apply must write an explicit false")
	}
}
