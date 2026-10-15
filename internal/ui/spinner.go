package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var braille = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	hideCursor = "\033[?25l"
	showCursor = "\033[?25h"
	clearLine  = "\r\033[K"
)

type spinnerHooks struct {
	mu       sync.Mutex
	tty      func() bool
	now      func() time.Time
	writer   io.Writer
	interval time.Duration
}

var hooks = spinnerHooks{
	tty:      stderrTTY,
	now:      time.Now,
	writer:   os.Stderr,
	interval: 80 * time.Millisecond,
}

func stderrTTY() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// TestHook injects the spinner writer, TTY flag, interval and clock for tests.
func TestHook(w io.Writer, tty bool, interval time.Duration, now func() time.Time) (restore func()) {
	hooks.mu.Lock()
	prevTTY, prevNow, prevW, prevI := hooks.tty, hooks.now, hooks.writer, hooks.interval
	hooks.tty = func() bool { return tty }
	if w != nil {
		hooks.writer = w
	}
	if interval > 0 {
		hooks.interval = interval
	}
	if now != nil {
		hooks.now = now
	}
	hooks.mu.Unlock()
	return func() {
		hooks.mu.Lock()
		hooks.tty, hooks.now, hooks.writer, hooks.interval = prevTTY, prevNow, prevW, prevI
		hooks.mu.Unlock()
	}
}

func hookWriter() io.Writer {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return hooks.writer
}

func hookTTY() bool {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return hooks.tty()
}

func hookNow() time.Time {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return hooks.now()
}

func hookInterval() time.Duration {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return hooks.interval
}

type spinner struct {
	w        io.Writer
	label    string
	delay    time.Duration
	interval time.Duration
	start    time.Time
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	frame    int
	shown    bool
}

// Start runs a braille spinner on stderr with label until the returned func is called.
func Start(label string) func() {
	return startAfter(label, 0)
}

// StartAfter waits delay before the first frame (store open uses 300ms).
func StartAfter(label string, delay time.Duration) func() {
	return startAfter(label, delay)
}

func startAfter(label string, delay time.Duration) func() {
	if !hookTTY() {
		return func() {}
	}
	s := &spinner{
		w:        hookWriter(),
		label:    label,
		delay:    delay,
		interval: hookInterval(),
		start:    hookNow(),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go s.loop()
	return s.Stop
}

func (s *spinner) loop() {
	defer close(s.done)
	if s.delay > 0 {
		t := time.NewTimer(s.delay)
		select {
		case <-t.C:
		case <-s.stop:
			t.Stop()
			return
		}
	}
	s.paint()
	if s.interval <= 0 {
		<-s.stop
		s.clear()
		return
	}
	tick := time.NewTicker(s.interval)
	defer tick.Stop()
	for {
		select {
		case <-s.stop:
			s.clear()
			return
		case <-tick.C:
			s.frame++
			s.paint()
		}
	}
}

func (s *spinner) paint() {
	elapsed := hookNow().Sub(s.start)
	msg := braille[s.frame%len(braille)] + " " + s.label
	if elapsed >= time.Second {
		msg += " " + formatElapsed(elapsed)
	}
	if !s.shown {
		fmt.Fprint(s.w, hideCursor)
		s.shown = true
	}
	fmt.Fprint(s.w, clearLine+msg)
}

func (s *spinner) clear() {
	if !s.shown {
		return
	}
	fmt.Fprint(s.w, clearLine+showCursor)
}

func (s *spinner) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
	})
}

func formatElapsed(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// Quiet starts a spinner after silence and restarts after each quiet gap.
type Quiet struct {
	label   string
	silence time.Duration
	mu      sync.Mutex
	stop    func()
	last    time.Time
	begin   time.Time
	quit    chan struct{}
	wg      sync.WaitGroup
	watchOn bool
	dead    bool
}

// NewQuiet watches for output; the spinner labeled label appears after silence.
func NewQuiet(label string, silence time.Duration) *Quiet {
	q := &Quiet{
		label:   label,
		silence: silence,
		begin:   hookNow(),
		quit:    make(chan struct{}),
	}
	if !hookTTY() {
		return q
	}
	q.watchOn = true
	q.wg.Add(1)
	go q.watch()
	return q
}

func (q *Quiet) watch() {
	defer q.wg.Done()
	poll := 50 * time.Millisecond
	if q.silence > 0 && q.silence/2 < poll {
		poll = q.silence / 2
		if poll < time.Millisecond {
			poll = time.Millisecond
		}
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-q.quit:
			return
		case <-tick.C:
			q.maybeStart()
		}
	}
}

func (q *Quiet) maybeStart() {
	if !hookTTY() {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.dead || q.stop != nil {
		return
	}
	ref := q.begin
	if !q.last.IsZero() {
		ref = q.last
	}
	if hookNow().Sub(ref) < q.silence {
		return
	}
	q.stop = Start(q.label)
}

// Write notes output and hides the spinner. It does not forward the bytes.
func (q *Quiet) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	q.mu.Lock()
	if q.stop != nil {
		q.stop()
		q.stop = nil
	}
	q.last = hookNow()
	q.mu.Unlock()
	return len(p), nil
}

func (q *Quiet) Stop() {
	q.mu.Lock()
	q.dead = true
	if q.stop != nil {
		q.stop()
		q.stop = nil
	}
	q.mu.Unlock()
	select {
	case <-q.quit:
	default:
		close(q.quit)
	}
	if q.watchOn {
		q.wg.Wait()
	}
}
