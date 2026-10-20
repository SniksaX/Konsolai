package theme

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	cursorMu    sync.Mutex
	cursorOn    = true
	cursorArmed bool
	cursorOut   io.Writer
	restoreOnce sync.Once
)

func SetCursorColor(on bool) {
	cursorMu.Lock()
	cursorOn = on
	cursorMu.Unlock()
}

func CursorColorOn() bool {
	cursorMu.Lock()
	defer cursorMu.Unlock()
	return cursorOn
}

func OSC12(c RGB) string {
	return fmt.Sprintf("\x1b]12;#%02X%02X%02X\x1b\\", c.R, c.G, c.B)
}

func OSC112() string { return "\x1b]112\x1b\\" }

func ApplyCursor(w io.Writer) {
	if w == nil || !CursorColorOn() || !Enabled() {
		return
	}
	c := RoleRGB(Heading)
	_, _ = io.WriteString(w, OSC12(c))
	cursorMu.Lock()
	cursorArmed = true
	cursorOut = w
	cursorMu.Unlock()
}

func RestoreCursor(w io.Writer) {
	cursorMu.Lock()
	armed := cursorArmed
	if w == nil {
		w = cursorOut
	}
	cursorArmed = false
	cursorMu.Unlock()
	if !armed {
		return
	}
	if w == nil {
		w = os.Stdout
	}
	_, _ = io.WriteString(w, OSC112())
}

// RestoreCursorAlways writes OSC 112 even if ApplyCursor was not seen.
// Used by tests and the panic/signal path so the terminal is never left tinted.
func RestoreCursorAlways(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	_, _ = io.WriteString(w, OSC112())
	cursorMu.Lock()
	cursorArmed = false
	cursorMu.Unlock()
}

func InstallCursorRestore(w io.Writer) (undo func()) {
	restoreOnce = sync.Once{}
	restore := func() {
		restoreOnce.Do(func() { RestoreCursorAlways(w) })
	}
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		_, ok := <-ch
		if ok {
			restore()
		}
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
		restore()
	}
}

func HeadingSeq() string {
	s := snap()
	if !s.enabled {
		return ""
	}
	return "\x1b[" + fgCode(s, roleColor(s.palette, s.appear, Heading)) + "m"
}
