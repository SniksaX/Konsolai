//go:build unix

package repl

import (
	"os"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"golang.org/x/sys/unix"
)

// readStdinByte waits up to d for one byte that readline has not already
// buffered. The wait is per byte, so a sequence split across writes still
// completes and a lone Esc does not.
func readStdinByte(d time.Duration) (byte, bool) {
	if d <= 0 {
		d = config.Builtin().EscTimeout()
	}
	fd := int(os.Stdin.Fd())
	deadline := time.Now().Add(d)
	buf := make([]byte, 1)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			return 0, false
		}
		ms := int(remain / time.Millisecond)
		if ms < 1 {
			ms = 1
		}
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(pfd, ms)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, false
		}
		if n == 0 {
			return 0, false
		}
		if pfd[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
			return 0, false
		}
		nread, err := os.Stdin.Read(buf)
		if nread == 1 {
			return buf[0], true
		}
		if err != nil {
			return 0, false
		}
	}
}
