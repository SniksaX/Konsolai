package commands

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestSetMenuWindowFitsTheTerminalHeight(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "100")
	rows := menuRows(config.Builtin())
	idx := selectable(rows)
	if len(idx) < 60 {
		t.Fatalf("the menu has only %d selectable rows", len(idx))
	}
	for _, height := range []int{4, 10, 24} {
		for _, pos := range []int{0, len(idx) / 2, len(idx) - 1} {
			text := formatSetMenuWindow(rows, idx[pos], false, height)
			if got := strings.Count(text, "\n"); got > height {
				t.Fatalf("height %d pos %d painted %d rows:\n%s", height, pos, got, text)
			}
			if !strings.Contains(text, "› ") {
				t.Fatalf("height %d pos %d lost the selection:\n%s", height, pos, text)
			}
			text = formatSetMenuWindow(rows, idx[pos], true, height)
			if got := strings.Count(text, "\n"); got > height {
				t.Fatalf("height %d pos %d detail painted %d rows:\n%s", height, pos, got, text)
			}
		}
	}
}

func TestSetMenuWindowMarkers(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLUMNS", "100")
	rows := menuRows(config.Builtin())
	idx := selectable(rows)
	top := formatSetMenuWindow(rows, idx[0], false, 24)
	if strings.Contains(top, "↑ ") || !strings.Contains(top, "↓ ") {
		t.Fatalf("top of the list should mark only the rows below:\n%s", top)
	}
	bottom := formatSetMenuWindow(rows, idx[len(idx)-1], false, 24)
	if !strings.Contains(bottom, "↑ ") || strings.Contains(bottom, "↓ ") {
		t.Fatalf("end of the list should mark only the rows above:\n%s", bottom)
	}
}
