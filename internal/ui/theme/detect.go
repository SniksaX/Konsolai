package theme

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

var (
	appearOnce Appearance
	appearSet  bool
)

func resolvedAppear() Appearance {
	mu.Lock()
	if appearSet && appearOnce != "" {
		a := appearOnce
		mu.Unlock()
		return a
	}
	mu.Unlock()
	return fromCOLORFGBG()
}

// DetectOnce queries the terminal background (OSC 11, 100ms) when appear is
// auto and stdout is a TTY. Call once at REPL start before readline.
func DetectOnce(tty *os.File) {
	mu.Lock()
	want := current.appear
	mu.Unlock()
	if want == Dark || want == Light {
		mu.Lock()
		appearOnce = want
		appearSet = true
		mu.Unlock()
		return
	}
	a := Dark
	if tty != nil && term.IsTerminal(int(tty.Fd())) {
		if q, ok := queryOSC11(tty, 100*time.Millisecond); ok {
			if RelLum(q) > 0.5 {
				a = Light
			} else {
				a = Dark
			}
		} else {
			a = fromCOLORFGBG()
		}
	} else {
		a = fromCOLORFGBG()
	}
	mu.Lock()
	appearOnce = a
	appearSet = true
	mu.Unlock()
}

func fromCOLORFGBG() Appearance {
	v := os.Getenv("COLORFGBG")
	if v == "" {
		return Dark
	}
	parts := strings.Split(v, ";")
	bg := strings.TrimSpace(parts[len(parts)-1])
	n, err := strconv.Atoi(bg)
	if err != nil {
		return Dark
	}
	if n == 7 || n == 15 {
		return Light
	}
	return Dark
}

func queryOSC11(f *os.File, timeout time.Duration) (RGB, bool) {
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return RGB{}, false
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return RGB{}, false
	}
	defer func() { _ = term.Restore(fd, old) }()
	if err := f.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return RGB{}, false
	}
	defer func() { _ = f.SetReadDeadline(time.Time{}) }()
	if _, err := f.WriteString("\x1b]11;?\x1b\\"); err != nil {
		return RGB{}, false
	}
	type result struct {
		c  RGB
		ok bool
	}
	ch := make(chan result, 1)
	go func() {
		r := bufio.NewReader(f)
		var buf []byte
		for {
			b, err := r.ReadByte()
			if err != nil {
				ch <- result{}
				return
			}
			buf = append(buf, b)
			if len(buf) > 2 && buf[len(buf)-1] == '\\' && buf[len(buf)-2] == 0x1b {
				c, ok := parseOSC11(string(buf))
				ch <- result{c, ok}
				return
			}
			if b == 0x07 {
				c, ok := parseOSC11(string(buf))
				ch <- result{c, ok}
				return
			}
			if len(buf) > 128 {
				ch <- result{}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		return res.c, res.ok
	case <-time.After(timeout):
		_ = f.SetReadDeadline(time.Now())
		select {
		case <-ch:
		case <-time.After(20 * time.Millisecond):
		}
		return RGB{}, false
	}
}

func parseOSC11(s string) (RGB, bool) {
	i := strings.Index(s, "rgb:")
	if i < 0 {
		return RGB{}, false
	}
	rest := s[i+4:]
	rest = strings.TrimRight(rest, "\a\x1b\\")
	parts := strings.Split(rest, "/")
	if len(parts) < 3 {
		return RGB{}, false
	}
	ch := func(p string) uint8 {
		p = strings.TrimSpace(p)
		if len(p) >= 2 {
			p = p[:2]
		}
		n, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return 0
		}
		return uint8(n)
	}
	return RGB{R: ch(parts[0]), G: ch(parts[1]), B: ch(parts[2])}, true
}
