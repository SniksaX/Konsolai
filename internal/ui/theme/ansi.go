package theme

const (
	esc        = "\x1b["
	ClearHome  = "\x1b[H\x1b[2J"
	ClearBelow = "\x1b[0J"
	HideCursor = "\x1b[?25l"
	ShowCursor = "\x1b[?25h"
	ClearLine  = "\r\x1b[K"
)

func CursorUp(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return esc + "1A"
	}
	return esc + itoa(n) + "A"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
