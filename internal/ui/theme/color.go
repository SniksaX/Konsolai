package theme

import (
	"fmt"
	"math"
)

type RGB struct{ R, G, B uint8 }

func hexRGB(h uint32) RGB {
	return RGB{R: uint8(h >> 16), G: uint8(h >> 8), B: uint8(h)}
}

func (c RGB) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

func to256(c RGB) int {
	q := func(v uint8) int { return (int(v)*5 + 127) / 255 }
	return 16 + 36*q(c.R) + 6*q(c.G) + q(c.B)
}

func linear(c uint8) float64 {
	x := float64(c) / 255
	if x <= 0.04045 {
		return x / 12.92
	}
	return math.Pow((x+0.055)/1.055, 2.4)
}

func RelLum(c RGB) float64 {
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

func Contrast(fg, bg RGB) float64 {
	a, b := RelLum(fg), RelLum(bg)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}
