package intent

import "testing"

func TestResolveLanguageCases(t *testing.T) {
	langs := []string{"en", "fr"}
	if got := ResolveLanguage("hiiii !", "", "es", "en", langs); got != "en" {
		t.Fatalf("hiiii: %q", got)
	}
	if got := ResolveLanguage("how are you ?", "hiiii !", "es", "en", langs); got != "en" {
		t.Fatalf("how are you after spanish: %q", got)
	}
	if got := ResolveLanguage("salut, ça va ?", "", "es", "en", langs); got != "fr" {
		t.Fatalf("salut: %q", got)
	}
	if got := ResolveLanguage("ok", "salut, ça va ?", "en", "en", langs); got != "fr" {
		t.Fatalf("ok after french: %q", got)
	}
	if got := ResolveLanguage("hiiii !", "", "es", "en", langs); got != "en" {
		t.Fatalf("router es ignored: %q", got)
	}
	if letterCount("hiiii !") >= 12 {
		t.Fatalf("hiiii should be a short greeting, letters=%d", letterCount("hiiii !"))
	}
}

func TestDetectRestrictedToProfile(t *testing.T) {
	if got := Detect("bonjour ça va bien merci", []string{"en"}); got != "" {
		t.Fatalf("fr not in profile, got %q", got)
	}
	if got := Detect("how are you doing today?", []string{"en", "fr"}); got != "en" {
		t.Fatalf("en %q", got)
	}
}

func TestLanguageName(t *testing.T) {
	if LanguageName("fr") != "French" || LanguageName("en") != "English" || LanguageName("es") != "English" {
		t.Fatalf("%q %q %q", LanguageName("fr"), LanguageName("en"), LanguageName("es"))
	}
}
