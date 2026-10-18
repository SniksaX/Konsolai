package commands

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func runStats(ctx context.Context, env *Env, args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(env.errw(), "usage: /stats [today|week|all]")
		return 2
	}
	window := "week"
	if len(args) == 1 {
		window = strings.ToLower(strings.TrimSpace(args[0]))
	}
	since, label, ok := statsSince(window, time.Now())
	if !ok {
		fmt.Fprintln(env.errw(), "usage: /stats [today|week|all]")
		return 2
	}
	if env == nil || env.Mem == nil || env.Mem.Store() == nil {
		if env != nil {
			fmt.Fprintln(env.out(), "stats need the database (memory is not configured)")
		}
		return 1
	}
	rep, err := env.Mem.Store().UsageReport(ctx, since)
	if err != nil {
		env.Mem.Fail(err)
		fmt.Fprintln(env.out(), "stats need the database (memory is not configured)")
		return 1
	}
	fmt.Fprint(env.out(), renderStats(label, rep))
	return 0
}

func statsSince(window string, now time.Time) (*time.Time, string, bool) {
	switch window {
	case "today":
		y, m, d := now.Date()
		t := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		return &t, "today", true
	case "week":
		t := now.Add(-7 * 24 * time.Hour)
		return &t, "week", true
	case "all":
		return nil, "all", true
	default:
		return nil, "", false
	}
}

func renderStats(window string, rep store.UsageReport) string {
	var b strings.Builder
	p := ui.Current()
	fmt.Fprintf(&b, "%s\n\n", p.Heading("stats  "+window))
	writeTable(&b, "models",
		[]string{"model", "calls", "prompt", "completion", "tok/s", "p50 ms", "p95 ms"},
		modelRows(rep.Models),
		[]bool{false, true, true, true, true, true, true})
	b.WriteByte('\n')
	writeTable(&b, "intents",
		[]string{"intent", "turns", "avg steps", "avg wall ms"},
		intentRows(rep.Intents),
		[]bool{false, true, true, true})
	b.WriteByte('\n')
	writeTable(&b, "totals",
		[]string{"sessions", "turns", "tokens"},
		[][]string{{
			strconv.Itoa(rep.Totals.Sessions),
			strconv.Itoa(rep.Totals.Turns),
			strconv.Itoa(rep.Totals.Tokens),
		}},
		[]bool{true, true, true})
	return b.String()
}

func modelRows(rows []store.ModelStat) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			r.Model,
			strconv.Itoa(r.Calls),
			strconv.Itoa(r.PromptTokens),
			strconv.Itoa(r.CompletionTokens),
			fmtRate(r.TokPerSec),
			fmtMS(r.P50MS),
			fmtMS(r.P95MS),
		}
	}
	return out
}

func intentRows(rows []store.IntentStat) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = []string{
			r.Intent,
			strconv.Itoa(r.Turns),
			strconv.FormatFloat(r.AvgSteps, 'f', 1, 64),
			fmtMS(r.AvgWallMS),
		}
	}
	return out
}

func fmtRate(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}

func fmtMS(v float64) string {
	return strconv.Itoa(int(math.Round(v)))
}

func writeTable(b *strings.Builder, title string, headers []string, rows [][]string, right []bool) {
	p := ui.Current()
	fmt.Fprintf(b, "%s\n", p.Heading(title))
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if i < len(widths) && len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	b.WriteString(paintRow(headers, widths, right, true))
	b.WriteByte('\n')
	for _, row := range rows {
		b.WriteString(paintRow(row, widths, right, false))
		b.WriteByte('\n')
	}
}

func paintRow(cells []string, widths []int, right []bool, header bool) string {
	p := ui.Current()
	parts := make([]string, len(cells))
	for i, c := range cells {
		w := 0
		if i < len(widths) {
			w = widths[i]
		}
		alignRight := i < len(right) && right[i]
		padded := padCell(c, w, alignRight)
		switch {
		case header:
			parts[i] = p.Heading(padded)
		case alignRight:
			parts[i] = p.Meta(padded)
		default:
			parts[i] = p.Muted(padded)
		}
	}
	return strings.Join(parts, "  ")
}

func padCell(s string, w int, right bool) string {
	if len(s) >= w {
		return s
	}
	pad := strings.Repeat(" ", w-len(s))
	if right {
		return pad + s
	}
	return s + pad
}
