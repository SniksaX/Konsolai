package banner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/ui"
)

func TestBannerWidth100ColorOff(t *testing.T) {
	var b bytes.Buffer
	info := Info{Version: "v0.1.0", Model: "qwen38.nvfp4", Dir: "~/proj"}
	if err := Render(&b, info, ui.Palette{Scheme: "amber"}, 100); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "\033") {
		t.Fatalf("ESC in color-off banner: %q", got)
	}
	want := readGolden(t, "width100.txt")
	if got != want {
		t.Fatalf("width 100\n got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(got, "v0.1.0   qwen38.nvfp4   ~/proj") {
		t.Fatalf("metadata: %q", got)
	}
	if !strings.Contains(got, hint) {
		t.Fatalf("hint: %q", got)
	}
}

func TestBannerWidth40Fallback(t *testing.T) {
	var b bytes.Buffer
	info := Info{Version: "v0.1.0", Model: "qwen38.nvfp4", Dir: "~/proj"}
	if err := Render(&b, info, ui.Palette{}, 40); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "\033") {
		t.Fatalf("ESC: %q", got)
	}
	want := readGolden(t, "width40.txt")
	if got != want {
		t.Fatalf("width 40\n got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "█") {
		t.Fatalf("fallback should not rasterize: %q", got)
	}
	if !strings.Contains(got, "> konsolai") {
		t.Fatalf("fallback: %q", got)
	}
}

func TestBannerNoESCWhenColorOff(t *testing.T) {
	var b bytes.Buffer
	if err := Render(&b, Info{Version: "v"}, ui.Palette{Scheme: "amber"}, 100); err != nil {
		t.Fatal(err)
	}
	if bytes.IndexByte(b.Bytes(), 0x1b) >= 0 {
		t.Fatalf("ESC bytes: %q", b.Bytes())
	}
	var on bytes.Buffer
	pal := ui.Palette{Enabled: true, Truecolor: true, Scheme: "amber"}
	if err := Render(&on, Info{Version: "v"}, pal, 100); err != nil {
		t.Fatal(err)
	}
	if bytes.IndexByte(on.Bytes(), 0x1b) < 0 {
		t.Fatal("expected SGR when color on")
	}
}

func TestStartupBannerFalse(t *testing.T) {
	var b bytes.Buffer
	err := Startup(&b, Info{Version: "v1.2", Model: "m"}, false, true, ui.Palette{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if got != "konsolai v1.2 · m · /help for commands\n" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "█") || strings.Contains(got, hint) {
		t.Fatalf("pixel splash leaked: %q", got)
	}
}

func TestStartupNotTTYUsesMutedLine(t *testing.T) {
	var b bytes.Buffer
	err := Startup(&b, Info{Version: "v", Model: ""}, true, false, ui.Palette{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != "konsolai v · - · /help for commands\n" {
		t.Fatalf("got %q", b.String())
	}
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
