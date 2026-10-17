package ui

import "strings"

func StripEmoji(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isEmojiRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return collapseSpaces(b.String())
}

func collapseSpaces(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' {
			if space {
				continue
			}
			space = true
			b.WriteByte(' ')
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

func isEmojiRune(r rune) bool {
	switch {
	case r == 0x200D || r == 0xFE0E || r == 0xFE0F || r == 0x20E3:
		return true
	case r >= 0x1F1E6 && r <= 0x1F1FF:
		return true
	case r >= 0x1F300 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2300 && r <= 0x23FF:
		return r == 0x231A || r == 0x231B || r == 0x23E9 || r == 0x23EA || r == 0x23EB || r == 0x23EC || r == 0x23F0 || r == 0x23F3
	case r >= 0x2B50 && r <= 0x2B55:
		return true
	case r >= 0xE0020 && r <= 0xE007F:
		return true
	default:
		return false
	}
}
