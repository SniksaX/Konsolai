package rag

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	tokensPerChar = 0.30
	turnMaxTokens = 1200
	webMaxTokens  = 600
	webOverlapTok = 80
)

var reHeading = regexp.MustCompile(`(?m)^#{1,6} `)

func estimateTokens(s string) int {
	return int(float64(utf8.RuneCountInString(s)) * tokensPerChar)
}

func runeWindow(maxTok, overlapTok int) (size, overlap int) {
	size = int(float64(maxTok) / tokensPerChar)
	if size < 1 {
		size = 1
	}
	overlap = int(float64(overlapTok) / tokensPerChar)
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size / 5
	}
	return size, overlap
}

func SplitTurn(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if estimateTokens(s) <= turnMaxTokens {
		return []string{s}
	}
	if parts := splitHeadings(s); len(parts) > 1 {
		return splitEach(parts, turnMaxTokens, 0)
	}
	if parts := splitParagraphs(s); len(parts) > 1 {
		return pack(parts, turnMaxTokens)
	}
	return windows(s, turnMaxTokens, 0)
}

func SplitWeb(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if estimateTokens(s) <= webMaxTokens {
		return []string{s}
	}
	if parts := splitHeadings(s); len(parts) > 1 {
		return splitEach(parts, webMaxTokens, webOverlapTok)
	}
	return windows(s, webMaxTokens, webOverlapTok)
}

func splitEach(parts []string, maxTok, overlap int) []string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if estimateTokens(p) <= maxTok {
			out = append(out, p)
			continue
		}
		out = append(out, windows(p, maxTok, overlap)...)
	}
	return out
}

func splitHeadings(s string) []string {
	idxs := reHeading.FindAllStringIndex(s, -1)
	if len(idxs) == 0 {
		return nil
	}
	var parts []string
	if idxs[0][0] > 0 {
		pre := strings.TrimSpace(s[:idxs[0][0]])
		if pre != "" {
			parts = append(parts, pre)
		}
	}
	for i, loc := range idxs {
		end := len(s)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		parts = append(parts, strings.TrimSpace(s[loc[0]:end]))
	}
	if len(parts) < 2 {
		return nil
	}
	return parts
}

func splitParagraphs(s string) []string {
	raw := strings.Split(s, "\n\n")
	var parts []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 2 {
		return nil
	}
	return parts
}

func pack(parts []string, maxTok int) []string {
	var out []string
	var cur strings.Builder
	for _, p := range parts {
		cand := p
		if cur.Len() > 0 {
			cand = cur.String() + "\n\n" + p
		}
		if cur.Len() > 0 && estimateTokens(cand) > maxTok {
			out = append(out, cur.String())
			cur.Reset()
			cur.WriteString(p)
			continue
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func windows(s string, maxTok, overlapTok int) []string {
	size, overlap := runeWindow(maxTok, overlapTok)
	runes := []rune(s)
	if len(runes) <= size {
		return []string{s}
	}
	step := size - overlap
	if step < 1 {
		step = 1
	}
	var out []string
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
	}
	return out
}
