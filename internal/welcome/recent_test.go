package welcome

import (
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestRecentNumbersAndOtherDir(t *testing.T) {
	now := time.Now()
	rows := []store.WelcomeRow{
		{Title: "here", Intent: "answer", CWD: "/proj", LastAt: now.Add(-time.Minute), Here: true},
		{Title: "away", Intent: "act", CWD: "/other", LastAt: now.Add(-time.Hour), Here: false},
	}
	got := recentFrom(rows, now)
	if len(got) != 2 || got[0].N != 1 || got[1].N != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Dir != "" || got[1].Dir != "/other" {
		t.Fatalf("dirs %+v", got)
	}
	line := recentText(ui.Current(), 80, 2, got[1])
	if !strings.Contains(line, "2 ") || !strings.Contains(line, "/other") || !strings.Contains(line, "away") {
		t.Fatalf("%q", line)
	}
	plain := recentText(ui.Current(), 80, 2, Recent{Title: "fix the welcome screen", Intent: "build", Age: "5m"})
	if strings.Contains(plain, "1 ") || strings.Contains(plain, "0 ") {
		t.Fatalf("unnumbered row gained a number %q", plain)
	}
}
