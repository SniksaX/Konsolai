package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestSetModelAddEachProviderPreset(t *testing.T) {
	for _, provider := range config.ProviderNames() {
		t.Run(provider, func(t *testing.T) {
			p, ok := config.Provider(provider)
			if !ok {
				t.Fatalf("provider %q is missing", provider)
			}
			env, out, errb := testEnv(t)
			args := []string{"model", "add", "pub", "--provider", provider, "--session"}
			if code := runSet(context.Background(), env, args); code != 0 {
				t.Fatalf("exit %d stderr %q", code, errb.String())
			}

			m, ok := env.Profile.Models["pub"]
			if !ok {
				t.Fatal("the model was not added to the profile")
			}
			if m.Endpoint != p.Endpoint || m.Context != p.Context || m.APIKeyEnv != p.APIKeyEnv {
				t.Fatalf("model %+v does not match preset %+v", m, p)
			}
			if m.MaxTokensField != p.MaxTokensField || m.SendsTemperature() != p.SendTemperature {
				t.Fatalf("request quirks %+v do not match preset %+v", m, p)
			}

			text := out.String()
			for _, want := range []string{
				p.Endpoint,
				fmt.Sprintf("ctx %d", p.Context),
				p.APIKeyEnv,
				"/set key pub",
				"from the next turn",
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("output %q is missing %q", text, want)
				}
			}
			if p.Note != "" {
				if !strings.Contains(text, "experimental: "+p.Note) {
					t.Fatalf("the %s note is missing from %q", provider, text)
				}
			} else if strings.Contains(text, "experimental") {
				t.Fatalf("provider %q printed a note without one: %q", provider, text)
			}
		})
	}
}
