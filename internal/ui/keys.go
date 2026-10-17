package ui

import (
	"io"
	"os"
	"sync"

	"golang.org/x/term"
)

// KeyReader returns the next user key from the input the REPL already owns.
type KeyReader func() (byte, error)

var (
	keysMu sync.Mutex
	keysFn KeyReader
)

// SetKeyReader installs the reader confirm uses instead of opening /dev/tty.
// Pass nil to restore the default stdin reader. The REPL sets this to
// readline's key reader so typed-ahead bytes are not lost.
func SetKeyReader(fn KeyReader) {
	keysMu.Lock()
	keysFn = fn
	keysMu.Unlock()
}

func currentKeyReader() KeyReader {
	keysMu.Lock()
	fn := keysFn
	keysMu.Unlock()
	return fn
}

func readStdinKey() (byte, error) {
	r := io.Reader(os.Stdin)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
		if err != nil {
			var b [1]byte
			n, rerr := os.Stdin.Read(b[:])
			if rerr != nil {
				return 0, rerr
			}
			if n == 0 {
				return 0, io.EOF
			}
			return b[0], nil
		}
		defer f.Close()
		r = f
		fd = int(f.Fd())
	}
	if term.IsTerminal(fd) {
		st, err := term.MakeRaw(fd)
		if err == nil {
			defer func() { _ = term.Restore(fd, st) }()
		}
	}
	var b [1]byte
	n, err := r.Read(b[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return b[0], nil
}
