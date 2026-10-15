package tools

import "fmt"

const captureWindow = 4 * 1024

type Capture struct {
	first []byte
	last  []byte
	n     int
}

func (c *Capture) Write(p []byte) (int, error) {
	n := len(p)
	c.n += n
	if len(c.first) < captureWindow {
		need := captureWindow - len(c.first)
		if len(p) <= need {
			c.first = append(c.first, p...)
			return n, nil
		}
		c.first = append(c.first, p[:need]...)
		p = p[need:]
	}
	c.last = append(c.last, p...)
	if len(c.last) > captureWindow {
		buf := make([]byte, captureWindow)
		copy(buf, c.last[len(c.last)-captureWindow:])
		c.last = buf
	}
	return n, nil
}

func (c *Capture) String() string {
	if c.n <= captureWindow {
		return string(c.first)
	}
	if c.n <= 2*captureWindow {
		return string(c.first) + string(c.last)
	}
	trunc := c.n - 2*captureWindow
	return string(c.first) + fmt.Sprintf("\n... [%d bytes truncated] ...\n", trunc) + string(c.last)
}
