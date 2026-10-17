package theme

import "sync"

type shaderKey struct {
	palette string
	appear  Appearance
	cols    int
}

var (
	shaderMu   sync.Mutex
	shaderTrue = map[shaderKey][][]RGB{}
	shader256  = map[shaderKey][]RGB{}
)

func ShaderGrid(palette string, appear Appearance, cols int) [][]RGB {
	if cols < 1 {
		cols = 1
	}
	palette = NormalizePalette(palette)
	if appear == Auto || appear == "" {
		appear = Dark
	}
	key := shaderKey{palette, appear, cols}
	shaderMu.Lock()
	if g, ok := shaderTrue[key]; ok {
		shaderMu.Unlock()
		return g
	}
	shaderMu.Unlock()
	top := roleColor(palette, appear, BannerTop)
	bot := roleColor(palette, appear, BannerBottom)
	last := float64(cols - 1)
	if last < 1 {
		last = 1
	}
	grid := make([][]RGB, 4)
	for row := 0; row < 4; row++ {
		grid[row] = make([]RGB, cols)
		for col := 0; col < cols; col++ {
			t := 0.65*float64(row)/3 + 0.35*float64(col)/last
			grid[row][col] = MixOKLab(top, bot, t)
		}
	}
	shaderMu.Lock()
	shaderTrue[key] = grid
	shaderMu.Unlock()
	return grid
}

func ShaderRows(palette string, appear Appearance) []RGB {
	palette = NormalizePalette(palette)
	if appear == Auto || appear == "" {
		appear = Dark
	}
	key := shaderKey{palette, appear, 0}
	shaderMu.Lock()
	if g, ok := shader256[key]; ok {
		shaderMu.Unlock()
		return g
	}
	shaderMu.Unlock()
	top := roleColor(palette, appear, BannerTop)
	bot := roleColor(palette, appear, BannerBottom)
	rows := make([]RGB, 4)
	for row := 0; row < 4; row++ {
		rows[row] = MixOKLab(top, bot, float64(row)/3)
	}
	shaderMu.Lock()
	shader256[key] = rows
	shaderMu.Unlock()
	return rows
}

func Gradient(n int, top, bot RGB) []RGB {
	if n < 1 {
		return nil
	}
	out := make([]RGB, n)
	last := float64(n - 1)
	if last < 1 {
		last = 1
	}
	for i := 0; i < n; i++ {
		out[i] = MixOKLab(top, bot, float64(i)/last)
	}
	return out
}

func PaintShaderLine(line string, colors []RGB) string {
	if line == "" || !snap().enabled {
		return line
	}
	var b []byte
	i := 0
	for _, r := range line {
		c := colors[0]
		if i < len(colors) {
			c = colors[i]
		}
		b = append(b, PaintRGB(c, string(r))...)
		i++
	}
	return string(b)
}
