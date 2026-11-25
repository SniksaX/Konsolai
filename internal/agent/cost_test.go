package agent

import (
	"strings"
	"testing"
	"time"
)

func pricedStats(remote bool, in, out float64) Stats {
	return Stats{
		Steps:            1,
		ModelTime:        17 * time.Second,
		PromptTokens:     2000,
		CompletionTokens: 1000,
		Pricing:          Pricing{Remote: remote, PriceIn: in, PriceOut: out},
	}
}

func TestFormatSummaryShowsRemoteCost(t *testing.T) {
	got := FormatSummary("answer", pricedStats(true, 2.5, 10))
	want := "17.0s · 2.0k → 1.0k tok · 59 tok/s · $0.015"
	if got != want {
		t.Fatalf("summary %q want %q", got, want)
	}
}

func TestFormatSummaryKeepsLocalAndUnpricedTurnsClean(t *testing.T) {
	local := FormatSummary("answer", pricedStats(false, 2.5, 10))
	if strings.Contains(local, "$") {
		t.Fatalf("local turn priced: %q", local)
	}
	unpriced := FormatSummary("answer", pricedStats(true, 0, 0))
	if strings.Contains(unpriced, "$") {
		t.Fatalf("unpriced remote turn priced: %q", unpriced)
	}
	if local != unpriced {
		t.Fatalf("local %q unpriced %q", local, unpriced)
	}
}

func TestFormatStatsLineShowsRemoteCostAndUsage(t *testing.T) {
	s := pricedStats(true, 2.5, 10)
	s.MaxStep = 1200 * time.Millisecond
	got := FormatStatsLine(s)
	want := "stats: steps=1 rejects=0 outside_plan=0 think_tokens=0 max_step_s=1.200 cost=$0.015 in=2.0k out=1.0k"
	if got != want {
		t.Fatalf("stats %q want %q", got, want)
	}
	for _, clean := range []Stats{
		pricedStats(false, 2.5, 10),
		pricedStats(true, 0, 0),
	} {
		if line := FormatStatsLine(clean); strings.Contains(line, "cost=") {
			t.Fatalf("cost shown for a clean turn: %q", line)
		}
	}
}

func TestCostUsesTheOnePriceThatIsSet(t *testing.T) {
	got := FormatSummary("build", pricedStats(true, 2.5, 0))
	if !strings.HasSuffix(got, "$0.005") {
		t.Fatalf("input-only price: %q", got)
	}
	got = FormatSummary("build", pricedStats(true, 0, 10))
	if !strings.HasSuffix(got, "$0.01") {
		t.Fatalf("output-only price: %q", got)
	}
}

func TestFormatUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.015, "$0.015"},
		{0.000001, "$0.000001"},
		{1.2345, "$1.2345"},
		{0, "$0.00"},
	}
	for _, c := range cases {
		if got := formatUSD(c.in); got != c.want {
			t.Fatalf("formatUSD(%v) = %q want %q", c.in, got, c.want)
		}
	}
}
