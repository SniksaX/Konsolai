package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func panelPlain(t *testing.T) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	restore := theme.Force(false, false, "amber", theme.Dark)
	t.Cleanup(restore)
}

const panelAnswer = `## Summary

Edited ` + "`internal/ui/panel.go`" + ` and the renderer.

Run it yourself:

` + "```sh\ngo test ./...\n```" + `
`

func TestAnswerPanelWidthsGolden(t *testing.T) {
	panelPlain(t)
	for _, width := range []int{60, 90, 140} {
		got := AnswerPanel("Done", panelAnswer, width, 40)
		path := filepath.Join("testdata", "answerpanel_w"+itoa(width)+".golden")
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
			t.Fatalf("missing %s: %v", path, err)
		}
		if got != string(want) {
			t.Fatalf("%s differs:\n%s", path, got)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestAnswerPanelNarrowUsesLeftRule(t *testing.T) {
	panelPlain(t)
	got := AnswerPanel("Done", "A short answer.", 40, 40)
	if strings.Contains(got, "╭") || !strings.Contains(got, "▌") {
		t.Fatalf("narrow panel must use a left rule:\n%s", got)
	}
}

func TestAnswerPanelJSONPrettyAndCap(t *testing.T) {
	panelPlain(t)
	fenced := "Here is the result:\n\n```json\n{\"b\":2,\"a\":[true,null,\"x\"]}\n```\n"
	got := AnswerPanel("Done", fenced, 90, 40)
	for _, want := range []string{"\"a\": [", "\"b\": 2", "true", "null", "\"x\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("pretty JSON misses %q:\n%s", want, got)
		}
	}
	whole := `{"z":1,"items":[1,2,3]}`
	got = AnswerPanel("Done", whole, 90, 40)
	if !strings.Contains(got, "\"items\": [") || !strings.Contains(got, "1") {
		t.Fatalf("whole-answer JSON not parsed:\n%s", got)
	}
	invalid := `{"broken": }`
	got = AnswerPanel("Done", invalid, 90, 40)
	if !strings.Contains(got, "broken") || !strings.Contains(got, "{") {
		t.Fatalf("invalid JSON was not left as is:\n%s", got)
	}
	var big []int
	for i := 0; i < 100; i++ {
		big = append(big, i)
	}
	raw, _ := json.Marshal(big)
	got = AnswerPanel("Done", string(raw), 90, 12)
	if !strings.Contains(got, "more lines") || strings.Count(got, "\n") > 20 {
		t.Fatalf("long JSON was not capped:\n%s", got)
	}
}

func TestAnswerPanelCommandsAndPaths(t *testing.T) {
	panelPlain(t)
	got := theme.StripANSI(AnswerPanel("Done", panelAnswer, 90, 40))
	if !strings.Contains(got, "go test ./...") {
		t.Fatalf("command block missing:\n%s", got)
	}
	if !strings.Contains(got, "Run it yourself") {
		t.Fatalf("command intro missing:\n%s", got)
	}
	if !strings.Contains(got, "internal/ui/panel.go") {
		t.Fatalf("path missing:\n%s", got)
	}
}

func TestPlainJSONHelperLeavesInvalidAlone(t *testing.T) {
	if _, ok := PrettyJSON("not json", 40); ok {
		t.Fatal("invalid JSON parsed")
	}
	got, ok := PrettyJSON(`{"a":1}`, 40)
	if !ok || !strings.Contains(got, "  \"a\": 1") {
		t.Fatalf("pretty %q %v", got, ok)
	}
}

func TestAnswerPanelAllowedNeedsATerminal(t *testing.T) {
	if _, _, ok := AnswerPanelAllowed(os.Stdout, 3); ok {
		t.Skip("stdout is a terminal in this environment")
	}
	if _, _, ok := AnswerPanelAllowed(nil, 3); ok {
		t.Fatal("nil writer must not allow the panel")
	}
}
