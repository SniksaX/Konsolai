package banner

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestBannerWidth100ColorOff(t *testing.T) {
	var b bytes.Buffer
	info := Info{Version: "v0.1.0", Model: "main-model", Dir: "~/projects/demo"}
	if err := Render(&b, info, ui.Palette{Scheme: "amber"}, 100, true); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "\033") {
		t.Fatalf("ESC in color-off banner: %q", got)
	}
	if !strings.Contains(got, "█") {
		t.Fatalf("missing wordmark:\n%s", got)
	}
	if !strings.Contains(got, "main-model") || !strings.Contains(got, "~/projects/demo") {
		t.Fatalf("metadata: %q", got)
	}
	if !strings.Contains(got, "/ commands") || !strings.Contains(got, "? help") {
		t.Fatalf("hint: %q", got)
	}
}

func TestBannerWidth40Fallback(t *testing.T) {
	var b bytes.Buffer
	info := Info{Version: "v0.1.0", Model: "main-model", Dir: "~/projects/demo"}
	if err := Render(&b, info, ui.Palette{}, 40, true); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "\033") {
		t.Fatalf("ESC: %q", got)
	}
	if strings.Contains(got, "█") {
		t.Fatalf("fallback should not rasterize: %q", got)
	}
	if !strings.Contains(got, "› konsolai") && !strings.Contains(got, "konsolai") {
		t.Fatalf("fallback: %q", got)
	}
}

func TestBannerNoESCWhenColorOff(t *testing.T) {
	var b bytes.Buffer
	if err := Render(&b, Info{Version: "v"}, ui.Palette{Scheme: "amber"}, 100, true); err != nil {
		t.Fatal(err)
	}
	if bytes.IndexByte(b.Bytes(), 0x1b) >= 0 {
		t.Fatalf("ESC bytes: %q", b.Bytes())
	}
	var on bytes.Buffer
	restore := ui.ForceTheme(ui.Palette{Enabled: true, Truecolor: true, Scheme: "amber"})
	defer restore()
	pal := ui.Palette{Enabled: true, Truecolor: true, Scheme: "amber"}
	if err := Render(&on, Info{Version: "v"}, pal, 100, true); err != nil {
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
	if strings.Contains(got, "█") {
		t.Fatalf("wordmark leaked: %q", got)
	}
	if !strings.Contains(got, "model") || !strings.Contains(got, "m") {
		t.Fatalf("card: %q", got)
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

func TestParseBuildAndHost(t *testing.T) {
	if got := ParseBuild("v0.1.0-369-ga2a4951"); got != "v0.1.0  a2a4951" {
		t.Fatalf("describe %q", got)
	}
	if got := ParseBuild("v0.1.0"); got != "v0.1.0" {
		t.Fatalf("tag %q", got)
	}
	if got := HostOf("http://gpu-box:8000/v1"); got != "gpu-box" {
		t.Fatalf("host %q", got)
	}
	c := Compact(Info{Version: "v0.1.0", Model: "main-model"}, ui.Palette{})
	if !strings.Contains(c, "KONSOLAI") || !strings.Contains(c, "v0.1.0") {
		t.Fatalf("compact %q", c)
	}
}
