package welcome

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/banner"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
)

func plain(t *testing.T) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	restore := theme.Force(false, false, "amber", theme.Dark)
	t.Cleanup(restore)
}

func renderString(d Data, layout string, width, height int) string {
	var b bytes.Buffer
	Render(&b, d, layout, width, height)
	return b.String()
}

func allUp() Data {
	return Data{
		Host: "fatboy",
		Models: []Model{
			{Name: "qwen38.nvfp4", Role: "main", Status: StatusUp, Stat: "84 tokens/s"},
			{Name: "qwen38-fast", Role: "fast", Status: StatusUp, Stat: "120 tokens/s"},
			{Name: "nomic-embed", Role: "embed", Status: StatusUp, Stat: "40ms"},
			{Name: "bge-reranker", Role: "rerank", Status: StatusUp, Stat: "18ms"},
		},
		Up: 4, Total: 4,
		Session: Session{
			Dir:     "~/proj",
			Branch:  "dev",
			Dirty:   true,
			Changed: 3,
			Ahead:   1,
			Behind:  2,
			Build:   "v0.1.0  ab12cd3",
			Memory:  "412 facts · 2 sessions",
		},
		Recent: []Recent{
			{Title: "fix the welcome screen before theme preview", Intent: "build", Age: "5m"},
			{Title: "lookup theme roles", Intent: "lookup", Age: "2h"},
			{Title: "answer a short question", Intent: "answer", Age: "1d"},
			{Title: "wire the footer meter", Intent: "act", Age: "3w"},
		},
		Placeholder: "ask anything, or /resume to continue fix the welcome screen",
		Tip:         "/undo reverts the last file change",
		Version:     "v0.1.0",
	}
}

func offline() Data {
	d := allUp()
	d.Up = 0
	for i := range d.Models {
		d.Models[i].Status = StatusDown
	}
	d.Models[0].Stat = ":11436 unreachable"
	d.Models[1].Stat = ":11437 unreachable"
	d.Models[2].Stat = ":8081 unreachable"
	d.Models[3].Stat = ":8082 unreachable"
	return d
}

func dbOffline() Data {
	d := allUp()
	d.Session.Memory = "memory offline"
	d.Recent = nil
	d.Placeholder = "ask anything"
	return d
}

func noGit() Data {
	d := allUp()
	d.Session.Branch = "not a git repo"
	d.Session.Dirty = false
	d.Session.Changed = 0
	d.Session.Ahead = 0
	d.Session.Behind = 0
	return d
}

func emptyHist() Data {
	d := allUp()
	d.Recent = nil
	d.Placeholder = "ask anything"
	return d
}

func fixtures() []struct {
	name string
	data Data
} {
	return []struct {
		name string
		data Data
	}{
		{"allup", allUp()},
		{"offline", offline()},
		{"dboffline", dbOffline()},
		{"nogit", noGit()},
		{"empty", emptyHist()},
	}
}

func TestRenderGoldens(t *testing.T) {
	plain(t)
	widths := []int{140, 120, 110, 90, 60}
	heights := []int{40, 26, 20}
	layouts := []string{"split", "frame"}
	for _, fx := range fixtures() {
		for _, w := range widths {
			for _, h := range heights {
				for _, layout := range layouts {
					got := renderString(fx.data, layout, w, h)
					if !widthOK(t, got, w) {
						t.Errorf("%s %s %dx%d exceeds width", layout, fx.name, w, h)
						continue
					}
					name := layout + "_" + fx.name + "_w" + itoa(w) + "_h" + itoa(h) + ".golden"
					path := filepath.Join("testdata", name)
					if os.Getenv("UPDATE_GOLDEN") == "1" {
						if err := os.MkdirAll("testdata", 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
							t.Fatal(err)
						}
						continue
					}
					want, err := os.ReadFile(path)
					if err != nil {
						t.Errorf("missing %s: %v", path, err)
						continue
					}
					if got != string(want) {
						t.Errorf("%s\n%s", path, firstDiff(got, string(want)))
					}
				}
			}
		}
	}
}

func TestWideSplitKeepsRecentBesideSession(t *testing.T) {
	plain(t)
	d := Data{
		Host: "fatboy",
		Models: []Model{
			{Name: "qwen38.nvfp4", Role: "main", Status: StatusUp, Stat: "126ms"},
			{Name: "fast", Role: "fast", Status: StatusUp, Stat: "105ms"},
			{Name: "embed", Role: "embed", Status: StatusUp, Stat: "225ms"},
			{Name: "rerank", Role: "rerank", Status: StatusDown, Stat: ":9501 unreachable"},
		},
		Up: 3, Total: 4,
		Session: Session{
			Dir:     "~/Desktop/other/Konsolai",
			Branch:  "dev",
			Dirty:   true,
			Changed: 1,
			Ahead:   232,
			Build:   "v0.1.0  05fc4c0",
			Memory:  "0 facts · 103 sessions",
		},
		Recent: []Recent{
			{Title: "fix the welcome screen", Intent: "build", Age: "5m"},
			{Title: "lookup theme roles", Intent: "lookup", Age: "2h"},
			{Title: "answer a short question", Intent: "answer", Age: "1d"},
			{Title: "wire the footer meter", Intent: "act", Age: "3w"},
		},
		Tip:     "!cmd runs a shell command",
		Version: "v0.1.0",
	}
	for _, w := range []int{200, 160, 215} {
		got := renderString(d, "split", w, 40)
		if !widthOK(t, got, w) {
			t.Fatalf("W=%d exceeds width", w)
		}
		if !strings.Contains(got, "3/4 up") || !strings.Contains(got, "/resume") {
			t.Fatalf("W=%d missing host or /resume\n%s", w, got)
		}
		side := false
		for _, ln := range strings.Split(got, "\n") {
			if strings.Contains(ln, "session") && strings.Contains(ln, "recent") {
				side = true
				break
			}
		}
		if !side {
			t.Fatalf("W=%d session and recent are not on the same row\n%s", w, got)
		}
	}
}

func TestFrameBelow120MatchesSplit(t *testing.T) {
	plain(t)
	for _, fx := range fixtures() {
		for _, w := range []int{119, 110, 90, 64, 63, 60, 40} {
			for _, h := range []int{40, 30, 26, 22, 20, 10} {
				frame := renderString(fx.data, "frame", w, h)
				split := renderString(fx.data, "split", w, h)
				if frame != split {
					t.Fatalf("frame != split at %dx%d %s\n%s", w, h, fx.name, firstDiff(frame, split))
				}
				if !widthOK(t, frame, max(w, 1)) && w > 0 {
					t.Fatalf("width %dx%d", w, h)
				}
			}
		}
	}
}

func TestUnknownLayoutMatchesSplit(t *testing.T) {
	plain(t)
	d := allUp()
	for _, layout := range []string{"", " ", "nope", "SPLIT"} {
		if renderString(d, layout, 140, 40) != renderString(d, "split", 140, 40) {
			t.Fatalf("layout %q did not fall back to split", layout)
		}
	}
}

func TestZeroData(t *testing.T) {
	plain(t)
	var d Data
	for _, layout := range []string{"", "split", "frame", "minimal", "nope"} {
		for _, w := range []int{0, 1, 20, 40, 63, 64, 90, 109, 110, 119, 120, 140, 200} {
			for _, h := range []int{0, 1, 10, 21, 22, 29, 30, 40} {
				got := renderString(d, layout, w, h)
				limit := w
				if limit <= 0 {
					limit = 80
				}
				if !widthOK(t, got, limit) {
					t.Fatalf("zero %s %dx%d exceeds", layout, w, h)
				}
			}
		}
	}
}

func TestMinimalMatchesBanner(t *testing.T) {
	plain(t)
	d := allUp()
	for _, w := range []int{40, 80, 100, 140} {
		var want bytes.Buffer
		if err := banner.Render(&want, bannerInfo(d), ui.Current(), w, true); err != nil {
			t.Fatal(err)
		}
		got := renderString(d, "minimal", w, 20)
		if got != want.String() {
			t.Fatalf("minimal %d\n%s", w, firstDiff(got, want.String()))
		}
	}
	out := renderString(d, "minimal", 100, 40)
	if !strings.Contains(out, "dev*") || !strings.Contains(out, "qwen38.nvfp4") || !strings.Contains(out, "/ commands") {
		t.Fatalf("minimal card missing fields:\n%s", out)
	}
}

func TestHeightAndWidthFallbacks(t *testing.T) {
	plain(t)
	d := allUp()
	wideShort := renderString(d, "split", 140, 20)
	if strings.Contains(wideShort, "█") || strings.Contains(wideShort, "/ commands") || strings.Contains(wideShort, "fix the welcome") {
		t.Fatalf("H=20 should be compact + strip only:\n%s", wideShort)
	}
	if !strings.Contains(wideShort, "KONSOLAI") || !strings.Contains(wideShort, "4/4 up") {
		t.Fatalf("H=20 header:\n%s", wideShort)
	}
	wideDrop := renderString(d, "split", 140, 26)
	if strings.Contains(wideDrop, "fix the welcome") || strings.Contains(wideDrop, "/resume") {
		t.Fatalf("H=26 should drop recent:\n%s", wideDrop)
	}
	if !strings.Contains(wideDrop, "█") || !strings.Contains(wideDrop, "/ commands") || !strings.Contains(wideDrop, "dev*") {
		t.Fatalf("H=26 split:\n%s", wideDrop)
	}
	full := renderString(d, "split", 140, 40)
	if !strings.Contains(full, "fix the welcome screen before theme preview") {
		t.Fatalf("wide title should fit:\n%s", full)
	}
	medium := renderString(d, "split", 110, 40)
	if strings.Contains(medium, "fix the welcome screen before theme preview") {
		t.Fatalf("medium title should truncate:\n%s", medium)
	}
	if !strings.Contains(medium, "build") || !strings.Contains(medium, "5m") {
		t.Fatalf("mode and age must stay visible:\n%s", medium)
	}
	narrow := renderString(d, "split", 90, 40)
	if !strings.Contains(narrow, "4/4 up") || !strings.Contains(narrow, "dev*") || !strings.Contains(narrow, "3 changed") {
		t.Fatalf("narrow strip:\n%s", narrow)
	}
	if !strings.Contains(narrow, "answer a short question") || strings.Contains(narrow, "wire the footer meter") {
		t.Fatalf("narrow list should keep three sessions:\n%s", narrow)
	}
	if !strings.Contains(full, "tip") || !strings.Contains(full, "/undo reverts the last file change") {
		t.Fatalf("wide hint should keep the tip:\n%s", full)
	}
	compact := renderString(d, "frame", 60, 40)
	if strings.Contains(compact, "█") || strings.Contains(compact, "fix the welcome") {
		t.Fatalf("W=60 compact:\n%s", compact)
	}
	if !strings.Contains(compact, "KONSOLAI") || !strings.Contains(compact, "/ commands") {
		t.Fatalf("W=60 hint:\n%s", compact)
	}
	if strings.Contains(compact, "/undo reverts the last file change") {
		t.Fatalf("W=60 should hide the tip:\n%s", compact)
	}
}

func TestHealthAndErrorWords(t *testing.T) {
	plain(t)
	if !strings.Contains(renderString(allUp(), "frame", 140, 40), "● fatboy online") {
		t.Fatal("all up")
	}
	off := renderString(offline(), "frame", 140, 40)
	if !strings.Contains(off, "○ fatboy offline") || !strings.Contains(off, "unreachable") {
		t.Fatalf("offline frame:\n%s", off)
	}
	db := renderString(dbOffline(), "split", 140, 40)
	if !strings.Contains(db, "memory offline") || !strings.Contains(db, "no sessions yet") {
		t.Fatalf("db offline:\n%s", db)
	}
	git := renderString(noGit(), "split", 110, 40)
	if !strings.Contains(git, "not a git repo") {
		t.Fatalf("nogit:\n%s", git)
	}
	empty := renderString(emptyHist(), "frame", 120, 40)
	if !strings.Contains(empty, "no sessions yet") || !strings.Contains(empty, "412 facts") {
		t.Fatalf("empty:\n%s", empty)
	}

	slow := allUp()
	slow.Models[1].Status = StatusSlow
	slow.Models[1].Stat = "1400ms"
	slow.Models[3].Status = StatusDown
	slow.Models[3].Stat = ":8082 unreachable"
	slow.Up = 2
	slow.Total = 4
	deg := renderString(slow, "frame", 140, 40)
	if !strings.Contains(deg, "● fatboy degraded") {
		t.Fatalf("degraded:\n%s", deg)
	}
	strip := renderString(slow, "split", 90, 40)
	if !strings.Contains(strip, "2/4 up") || !strings.Contains(strip, "bge-reranker") || !strings.Contains(strip, "qwen38-fast") {
		t.Fatalf("degraded strip:\n%s", strip)
	}

	restore := theme.Force(true, true, "amber", theme.Dark)
	defer restore()
	var colored bytes.Buffer
	Render(&colored, offline(), "split", 140, 40)
	if !strings.Contains(colored.String(), theme.Style(theme.Error, "unreachable")) {
		t.Fatal("unreachable not painted Error")
	}
	var frameOff bytes.Buffer
	Render(&frameOff, offline(), "frame", 140, 40)
	if !strings.Contains(frameOff.String(), theme.Style(theme.Error, "○ fatboy offline")) {
		t.Fatal("offline banner not painted Error")
	}
	var mem bytes.Buffer
	Render(&mem, dbOffline(), "split", 90, 40)
	if !strings.Contains(mem.String(), theme.Style(theme.Error, "offline")) {
		t.Fatal("memory offline not painted Error")
	}
	timed := allUp()
	timed.Models[2].Status = StatusTimeout
	timed.Models[2].Stat = "timeout"
	timed.Up = 3
	var timeout bytes.Buffer
	Render(&timeout, timed, "split", 140, 40)
	if !strings.Contains(timeout.String(), theme.Style(theme.Error, "timeout")) {
		t.Fatal("timeout not painted Error")
	}
	var up bytes.Buffer
	Render(&up, allUp(), "split", 140, 40)
	if !strings.Contains(up.String(), theme.Style(theme.Success, "●")) {
		t.Fatal("up dot not Success")
	}
	var warn bytes.Buffer
	Render(&warn, slow, "split", 140, 40)
	if !strings.Contains(warn.String(), theme.Style(theme.Warn, "●")) {
		t.Fatal("slow dot not Warn")
	}
	if !strings.Contains(warn.String(), theme.Style(theme.Error, "○")) {
		t.Fatal("down dot not Error")
	}
}

func TestColorIsPaintOnly(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	samples := []struct {
		layout string
		w, h   int
	}{
		{"split", 140, 40},
		{"frame", 140, 40},
		{"split", 110, 40},
		{"frame", 110, 26},
		{"split", 90, 40},
		{"frame", 60, 20},
		{"minimal", 100, 40},
	}
	for _, fx := range fixtures() {
		for _, s := range samples {
			off := theme.Force(false, false, "amber", theme.Dark)
			plainOut := renderString(fx.data, s.layout, s.w, s.h)
			off()
			on := theme.Force(true, true, "amber", theme.Dark)
			colored := renderString(fx.data, s.layout, s.w, s.h)
			on()
			if theme.StripANSI(colored) != plainOut {
				t.Fatalf("color changed text %s %s %dx%d\n%s", s.layout, fx.name, s.w, s.h, firstDiff(theme.StripANSI(colored), plainOut))
			}
			if !widthOK(t, colored, s.w) {
				t.Fatalf("colored width %s %s %dx%d", s.layout, fx.name, s.w, s.h)
			}
			if !strings.Contains(colored, "\x1b[") && s.w >= 80 {
				t.Fatalf("expected SGR %s %s %dx%d", s.layout, fx.name, s.w, s.h)
			}
		}
	}
}

func TestDrawBoxWidth(t *testing.T) {
	plain(t)
	pal := ui.Current()
	for _, w := range []int{24, 42, 52, 67, 72, 120, 140} {
		box := drawBox(pal, w, "fatboy", "4/4 up", pal.Muted, modelRows(pal, w-2, allUp().Models))
		for _, ln := range box {
			if got := theme.DisplayWidth(ln); got != w {
				t.Fatalf("box width %d line %d\n%q", w, got, ln)
			}
		}
		plainBox := drawBox(pal, w, "session", "", nil, sessionLines(pal, w-2, allUp().Session))
		for _, ln := range plainBox {
			if got := theme.DisplayWidth(ln); got != w {
				t.Fatalf("session box width %d line %d\n%q", w, got, ln)
			}
		}
	}
}

func TestFrameEdgesFullWidth(t *testing.T) {
	plain(t)
	for _, w := range []int{120, 140, 160} {
		got := renderString(allUp(), "frame", w, 40)
		for i, ln := range strings.Split(got, "\n") {
			plainLn := theme.StripANSI(ln)
			if plainLn == "" {
				continue
			}
			if strings.ContainsAny(plainLn, "╭╰├┤│") {
				if theme.DisplayWidth(ln) != w {
					t.Fatalf("frame %d line %d width %d\n%q", w, i+1, theme.DisplayWidth(ln), ln)
				}
			}
		}
		split := renderString(allUp(), "split", w, 40)
		saw := false
		for _, ln := range strings.Split(split, "\n") {
			if !strings.Contains(ln, "╭") || !strings.ContainsAny(ln, "█▀▄") {
				continue
			}
			saw = true
			if theme.DisplayWidth(ln) != w {
				t.Fatalf("split banner row %d width %d\n%q", w, theme.DisplayWidth(ln), ln)
			}
		}
		if !saw {
			t.Fatalf("split %d has no banner row beside the host box", w)
		}
	}
}

func TestPlaceholderNotPrinted(t *testing.T) {
	plain(t)
	d := allUp()
	d.Placeholder = "ask anything, or /resume to continue UNIQUE_PLACEHOLDER"
	out := renderString(d, "split", 140, 40) + renderString(d, "frame", 140, 40)
	if strings.Contains(out, "UNIQUE_PLACEHOLDER") {
		t.Fatal("placeholder leaked onto the welcome screen")
	}
}

func widthOK(t *testing.T, s string, width int) bool {
	t.Helper()
	if width <= 0 {
		t.Fatalf("width %d", width)
	}
	ok := true
	for i, ln := range strings.Split(s, "\n") {
		if theme.DisplayWidth(ln) > width {
			t.Errorf("line %d width %d > %d: %q", i+1, theme.DisplayWidth(ln), width, ln)
			ok = false
		}
	}
	return ok
}

func firstDiff(got, want string) string {
	gl := strings.Split(got, "\n")
	wl := strings.Split(want, "\n")
	n := len(gl)
	if len(wl) > n {
		n = len(wl)
	}
	var b strings.Builder
	shown := 0
	for i := 0; i < n && shown < 6; i++ {
		gs, ws := "", ""
		if i < len(gl) {
			gs = gl[i]
		}
		if i < len(wl) {
			ws = wl[i]
		}
		if gs == ws {
			continue
		}
		b.WriteString("line " + itoa(i+1) + "\n got (" + itoa(theme.DisplayWidth(gs)) + "): " + gs + "\n want (" + itoa(theme.DisplayWidth(ws)) + "): " + ws + "\n")
		shown++
	}
	if len(gl) != len(wl) {
		b.WriteString("got " + itoa(len(gl)) + " lines, want " + itoa(len(wl)) + "\n")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
