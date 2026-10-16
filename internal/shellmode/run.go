package shellmode

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
)

func RunPty(ctx context.Context, cwd, line string, stdin *os.File, stdout io.Writer) (exit int, dur time.Duration, capture string, err error) {
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	cmd := exec.CommandContext(ctx, "bash", "-c", line)
	cmd.Dir = cwd
	start := time.Now()
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return 1, time.Since(start), "", err
	}
	defer ptmx.Close()

	_ = pty.InheritSize(stdin, ptmx)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-winch:
				_ = pty.InheritSize(stdin, ptmx)
			case <-done:
				return
			}
		}
	}()
	defer close(done)
	defer signal.Stop(winch)

	old, rawErr := makeRaw(int(stdin.Fd()))
	if rawErr == nil && old != nil {
		defer restore(int(stdin.Fd()), old)
	}

	capw := newCapWriter(stdout, captureCap)
	go func() {
		_, _ = io.Copy(ptmx, stdin)
	}()
	_, _ = io.Copy(capw, ptmx)
	waitErr := cmd.Wait()
	dur = time.Since(start)
	exit = 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
			waitErr = nil
		} else {
			err = waitErr
			exit = 1
		}
	}
	return exit, dur, capw.String(), err
}

type capWriter struct {
	w     io.Writer
	max   int
	buf   []byte
	full  bool
	tail  []byte
	tpos  int
	tfill int
}

func newCapWriter(w io.Writer, max int) *capWriter {
	if max < 2 {
		max = 2
	}
	return &capWriter{w: w, max: max}
}

func (c *capWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		c.add(p[:n])
	}
	return n, err
}

func (c *capWriter) add(p []byte) {
	if !c.full {
		if len(c.buf)+len(p) <= c.max {
			c.buf = append(c.buf, p...)
			return
		}
		need := c.max - len(c.buf)
		if need > 0 {
			c.buf = append(c.buf, p[:need]...)
			p = p[need:]
		}
		c.full = true
		c.tail = make([]byte, c.max/2)
	}
	for _, b := range p {
		c.tail[c.tpos] = b
		c.tpos = (c.tpos + 1) % len(c.tail)
		if c.tfill < len(c.tail) {
			c.tfill++
		}
	}
}

func (c *capWriter) String() string {
	if !c.full {
		return CapHeadTail(string(c.buf), c.max)
	}
	keep := c.max / 2
	head := c.buf
	if len(head) > keep {
		head = head[:keep]
	}
	var tail []byte
	if c.tfill < len(c.tail) {
		tail = c.tail[:c.tfill]
	} else {
		tail = append(append([]byte{}, c.tail[c.tpos:]...), c.tail[:c.tpos]...)
	}
	return string(head) + truncateMark + string(tail)
}
