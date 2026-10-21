package theme

const (
	esc         = "\x1b["
	ClearHome   = "\x1b[H\x1b[2J"
	ClearScroll = "\x1b[H\x1b[2J\x1b[3J"
	ResetScroll = "\x1b[r"
	ClearBelow  = "\x1b[0J"
	HideCursor  = "\x1b[?25l"
	ShowCursor  = "\x1b[?25h"
	ClearLine   = "\r\x1b[K"
)

// InputEcho paints a submitted line in the heading color, without bold.
func InputEcho(text string) string {
	return Style(Heading, text)
}

// ScrollRegion limits scrolling to rows top through bottom, 1-based.
// The lines above top stay fixed. ResetScroll restores the full screen.
// Setting a region moves the cursor to the top-left corner, so the cursor is
// saved and restored around it; otherwise the next write lands on the header.
func ScrollRegion(top, bottom int) string {
	if top < 1 || bottom < top {
		return ""
	}
	return "\x1b7" + esc + itoa(top) + ";" + itoa(bottom) + "r" + "\x1b8"
}

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
