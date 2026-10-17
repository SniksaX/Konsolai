package theme

import "math"

// Lab is Ottosson OKLab (L, a, b).
type Lab struct{ L, A, B float64 }

func RGBToOKLab(c RGB) Lab {
	r := linear(c.R)
	g := linear(c.G)
	b := linear(c.B)
	l := 0.4122214708*r + 0.5363325363*g + 0.0514459929*b
	m := 0.2119034982*r + 0.6806995451*g + 0.1073969566*b
	s := 0.0883024619*r + 0.2817188376*g + 0.6299787005*b
	l_ := cbrt(l)
	m_ := cbrt(m)
	s_ := cbrt(s)
	return Lab{
		L: 0.2104542553*l_ + 0.7936177850*m_ - 0.0040720468*s_,
		A: 1.9779984951*l_ - 2.4285922050*m_ + 0.4505937099*s_,
		B: 0.0259040371*l_ + 0.7827717662*m_ - 0.8086757660*s_,
	}
}

func OKLabToRGB(lab Lab) RGB {
	l_ := lab.L + 0.3963377774*lab.A + 0.2158037573*lab.B
	m_ := lab.L - 0.1055613458*lab.A - 0.0638541728*lab.B
	s_ := lab.L - 0.0894841775*lab.A - 1.2914855480*lab.B
	l := l_ * l_ * l_
	m := m_ * m_ * m_
	s := s_ * s_ * s_
	r := 4.0767416621*l - 3.3077115913*m + 0.2309699292*s
	g := -1.2684380046*l + 2.6097574011*m - 0.3413193965*s
	b := -0.0041960863*l - 0.7034186147*m + 1.7076147010*s
	return RGB{R: srgb8(r), G: srgb8(g), B: srgb8(b)}
}

func MixOKLab(a, b RGB, t float64) RGB {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	la, lb := RGBToOKLab(a), RGBToOKLab(b)
	return OKLabToRGB(Lab{
		L: la.L + (lb.L-la.L)*t,
		A: la.A + (lb.A-la.A)*t,
		B: la.B + (lb.B-la.B)*t,
	})
}

func OKLabDist(a, b Lab) float64 {
	dL := a.L - b.L
	dA := a.A - b.A
	dB := a.B - b.B
	return math.Sqrt(dL*dL + dA*dA + dB*dB)
}

func Chroma(c RGB) float64 {
	lab := RGBToOKLab(c)
	return math.Hypot(lab.A, lab.B)
}

func adjustLightness(c RGB, dL float64) RGB {
	lab := RGBToOKLab(c)
	lab.L = clamp01(lab.L + dL)
	return OKLabToRGB(lab)
}

func cbrt(x float64) float64 {
	if x < 0 {
		return -math.Pow(-x, 1.0/3.0)
	}
	return math.Pow(x, 1.0/3.0)
}

func srgb8(lin float64) uint8 {
	lin = clamp01(lin)
	var x float64
	if lin <= 0.0031308 {
		x = 12.92 * lin
	} else {
		x = 1.055*math.Pow(lin, 1.0/2.4) - 0.055
	}
	return uint8(math.Round(clamp01(x) * 255))
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
