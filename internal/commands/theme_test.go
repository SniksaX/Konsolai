package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/ui/theme"
)

func TestThemeListAndPersist(t *testing.T) {
	env, out, errb := testEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	src := `# keep
defaults:
  ui:
    palette: amber   # look
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	env.ConfigPath = path
	if code := runTheme(context.Background(), env, nil); code != 0 {
		t.Fatalf("list %d %s", code, errb.String())
	}
	listed := out.String()
	for _, name := range theme.PaletteNames {
		if !strings.Contains(listed, name) {
			t.Fatalf("missing %s in %q", name, listed)
		}
	}
	if !strings.Contains(listed, "◆ act") || !strings.Contains(listed, "accent") || !strings.Contains(listed, "muted") {
		t.Fatalf("preview %q", listed)
	}
	out.Reset()
	if code := runTheme(context.Background(), env, []string{"rose"}); code != 0 {
		t.Fatalf("apply %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "palette rose") {
		t.Fatalf("apply print %q", out.String())
	}
	if env.Profile.PaletteName() != "rose" {
		t.Fatalf("live %q", env.Profile.PaletteName())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "# keep") || !strings.Contains(s, "palette: rose") || !strings.Contains(s, "# look") {
		t.Fatalf("persist:\n%s", s)
	}
}

func TestThemeUnknown(t *testing.T) {
	env, _, errb := testEnv(t)
	if code := runTheme(context.Background(), env, []string{"neon"}); code != 2 {
		t.Fatalf("%d %s", code, errb.String())
	}
}
