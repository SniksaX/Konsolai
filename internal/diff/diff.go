package diff

import (
	"fmt"
	"strings"
)

const maxLines = 5000

func Unified(path, oldText, newText string, context int) string {
	if oldText == newText {
		return ""
	}
	oldLines, oldNoNL := splitLines(oldText)
	newLines, newNoNL := splitLines(newText)
	if len(oldLines) > maxLines || len(newLines) > maxLines {
		return fmt.Sprintf("(diff too large: %d -> %d lines)", len(oldLines), len(newLines))
	}
	if context < 0 {
		context = 0
	}
	edits := ses(oldLines, newLines)
	hunks := groupHunks(edits, context)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	oi, ni := 1, 1
	oldN, newN := len(oldLines), len(newLines)
	for _, h := range hunks {
		oldStart, newStart := oi, ni
		oldCount, newCount := 0, 0
		for _, e := range h {
			switch e.kind {
			case eq, del:
				oldCount++
			}
			switch e.kind {
			case eq, ins:
				newCount++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", zeroStart(oldStart, oldCount), oldCount, zeroStart(newStart, newCount), newCount)
		for i, e := range h {
			switch e.kind {
			case eq:
				b.WriteString(" " + e.line + "\n")
				oi++
				ni++
			case del:
				b.WriteString("-" + e.line + "\n")
				if oldNoNL && oi == oldN {
					b.WriteString("\\ No newline at end of file\n")
				}
				oi++
			case ins:
				b.WriteString("+" + e.line + "\n")
				if newNoNL && ni == newN {
					b.WriteString("\\ No newline at end of file\n")
				}
				ni++
			}
			_ = i
		}
	}
	return b.String()
}

func Stat(oldText, newText string) (added, removed int) {
	if oldText == newText {
		return 0, 0
	}
	oldLines, _ := splitLines(oldText)
	newLines, _ := splitLines(newText)
	for _, e := range ses(oldLines, newLines) {
		switch e.kind {
		case ins:
			added++
		case del:
			removed++
		}
	}
	return added, removed
}

func zeroStart(start, count int) int {
	if count == 0 {
		return start - 1
	}
	return start
}

func splitLines(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	missing := !strings.HasSuffix(s, "\n")
	parts := strings.Split(s, "\n")
	if !missing {
		parts = parts[:len(parts)-1]
	}
	return parts, missing
}

type kind int

const (
	eq kind = iota
	ins
	del
)

type edit struct {
	kind kind
	line string
}

func ses(a, b []string) []edit {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	trace, offset := shortestEdit(a, b)
	return backtrack(a, b, trace, offset)
}

func shortestEdit(a, b []string) ([][]int, int) {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil, 0
	}
	v := make([]int, 2*max+1)
	offset := max
	trace := make([][]int, 0, max+1)
	for d := 0; d <= max; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return trace, offset
			}
		}
	}
	return trace, offset
}

func backtrack(a, b []string, trace [][]int, offset int) []edit {
	x, y := len(a), len(b)
	var rev []edit
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, edit{eq, a[x]})
		}
		if d == 0 {
			break
		}
		if x == prevX {
			y--
			rev = append(rev, edit{ins, b[y]})
		} else {
			x--
			rev = append(rev, edit{del, a[x]})
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func groupHunks(edits []edit, context int) [][]edit {
	type rng struct{ lo, hi int }
	var changed []rng
	i := 0
	for i < len(edits) {
		if edits[i].kind == eq {
			i++
			continue
		}
		start := i
		for i < len(edits) && edits[i].kind != eq {
			i++
		}
		changed = append(changed, rng{start, i})
	}
	if len(changed) == 0 {
		return nil
	}
	var hunks [][]edit
	hLo := max(0, changed[0].lo-context)
	hHi := min(len(edits), changed[0].hi+context)
	for _, c := range changed[1:] {
		nextLo := max(0, c.lo-context)
		if nextLo <= hHi {
			hHi = min(len(edits), c.hi+context)
			continue
		}
		hunks = append(hunks, edits[hLo:hHi])
		hLo = nextLo
		hHi = min(len(edits), c.hi+context)
	}
	hunks = append(hunks, edits[hLo:hHi])
	return hunks
}
