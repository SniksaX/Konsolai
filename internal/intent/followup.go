package intent

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	reOptionNum = regexp.MustCompile(`(?i)\boption\s*\d+\b`)
	reOrdinal   = regexp.MustCompile(`(?i)\b(the\s+)?(first|second|third|1st|2nd|3rd|premier|premiere|première|deuxieme|deuxième|troisieme|troisième)\b`)
	reListItem  = regexp.MustCompile(`(?m)^\s*(\d+[\.)]|[-*])\s+\S`)
	reYesWord   = regexp.MustCompile(`(?i)^(yes|yeah|yep|oui|ouais|ok|okay)\b`)
)

func WordCount(s string) int {
	n := 0
	in := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			in = false
			continue
		}
		if !in {
			n++
			in = true
		}
	}
	return n
}

func OfferedQuestionOrOptions(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasSuffix(s, "?") {
		return true
	}
	if reListItem.MatchString(s) {
		return true
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, " or ") || strings.Contains(lower, " ou ") {
		return true
	}
	return false
}

func IsOptionReply(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if reYesWord.MatchString(s) {
		return true
	}
	if reOptionNum.MatchString(s) {
		return true
	}
	if reOrdinal.MatchString(s) {
		return true
	}
	return false
}

func HoldIntent(prev Intent, prevAssistant, msg string) bool {
	switch prev {
	case Act, Build, Lookup:
	default:
		return false
	}
	if !OfferedQuestionOrOptions(prevAssistant) {
		return false
	}
	if IsOptionReply(msg) {
		return true
	}
	return WordCount(msg) > 0 && WordCount(msg) <= 12
}

func WithPreviousExchange(msg, prevUser, prevAssistant string) string {
	msg = strings.TrimSpace(msg)
	if strings.TrimSpace(prevUser) == "" && strings.TrimSpace(prevAssistant) == "" {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	b.WriteString("\n\n<previous_exchange>\n")
	if strings.TrimSpace(prevUser) != "" {
		b.WriteString("User: ")
		b.WriteString(strings.TrimSpace(prevUser))
		b.WriteByte('\n')
	}
	if strings.TrimSpace(prevAssistant) != "" {
		b.WriteString("Assistant: ")
		b.WriteString(strings.TrimSpace(prevAssistant))
		b.WriteByte('\n')
	}
	b.WriteString("</previous_exchange>")
	return b.String()
}
