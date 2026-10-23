package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/store"
)

func TestStatsNeedsDatabase(t *testing.T) {
	env, out, errb := testEnv(t)
	if code := runStats(context.Background(), env, nil); code != 1 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "stats need the database (memory is not configured)" {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	if code := runStats(context.Background(), env, []string{"month"}); code != 2 {
		t.Fatalf("usage %d", code)
	}
	if !strings.Contains(errb.String(), "usage: /stats") {
		t.Fatalf("%q", errb.String())
	}
}

func TestStatsRenderGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	rate := 100.0
	slow := 10.0
	got := renderStats("week", store.UsageReport{
		Models: []store.ModelStat{
			{Model: "main-model", Calls: 2, PromptTokens: 20, CompletionTokens: 150, TokPerSec: &rate, P50MS: 500, P95MS: 1000},
			{Model: "other", Calls: 1, PromptTokens: 10, CompletionTokens: 10, TokPerSec: &slow, P50MS: 1000, P95MS: 1000},
		},
		Intents: []store.IntentStat{
			{Intent: "answer", Turns: 2, AvgSteps: 2, AvgWallMS: 750},
			{Intent: "lookup", Turns: 1, AvgSteps: 1, AvgWallMS: 1000},
		},
		Totals: store.UsageTotals{Sessions: 1, Turns: 2, Tokens: 170},
	})
	want, err := os.ReadFile(filepath.Join("testdata", "stats_week.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch:\n%s", got)
	}
}

func TestStatsRenderThinkColumn(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	got := renderStats("week", store.UsageReport{
		Models: []store.ModelStat{
			{Model: "main-model", Calls: 1, PromptTokens: 10, CompletionTokens: 20, ReasoningTokens: 7100},
		},
	})
	if !strings.Contains(got, "think") || !strings.Contains(got, "7100") {
		t.Fatalf("%s", got)
	}
}

func TestPadCellUsesDisplayWidth(t *testing.T) {
	if got := padCell("ab…", 5, false); got != "ab…  " {
		t.Fatalf("left %q", got)
	}
	if got := padCell("日本", 6, true); got != "  日本" {
		t.Fatalf("right %q", got)
	}
}
