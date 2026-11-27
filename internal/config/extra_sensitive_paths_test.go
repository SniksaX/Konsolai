package config

import (
	"strings"
	"testing"
)

func TestExtraSensitivePathsKey(t *testing.T) {
	k, ok := KeyByPath("safety.extra_sensitive_paths")
	if !ok {
		t.Fatal("safety.extra_sensitive_paths is not in the key table")
	}
	if k.Kind != KindList {
		t.Fatalf("kind %v, want list", k.Kind)
	}
	if k.Doc == "" {
		t.Fatal("key has no doc")
	}
	if got := Builtin().Safety.ExtraSensitivePaths; len(got) != 0 {
		t.Fatalf("built-in default %v", got)
	}
	c := Builtin()
	k.SetList(&c, []string{"infra/**", "Makefile"})
	if got := k.GetList(c); len(got) != 2 || got[0] != "infra/**" || got[1] != "Makefile" {
		t.Fatalf("get %v", got)
	}
	// The setter copies, so the caller's slice cannot mutate the profile.
	values := []string{"*.sh"}
	k.SetList(&c, values)
	values[0] = "changed"
	if got := c.Safety.ExtraSensitivePaths[0]; got != "*.sh" {
		t.Fatalf("setter did not copy: %q", got)
	}
}

func TestExtraSensitivePathsLoadAndRender(t *testing.T) {
	body := `defaults:
  safety:
    extra_sensitive_paths:
      - "infra/**"
      - "Makefile"
`
	f, err := Parse([]byte(body), "extra.yaml")
	if err != nil {
		t.Fatal(err)
	}
	prof, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	got := prof.Safety.ExtraSensitivePaths
	if len(got) != 2 || got[0] != "infra/**" || got[1] != "Makefile" {
		t.Fatalf("loaded %v", got)
	}
	raw := InitYAML()
	if !strings.Contains(raw, "extra_sensitive_paths: []") {
		t.Fatal("generated config does not document the key")
	}
}
