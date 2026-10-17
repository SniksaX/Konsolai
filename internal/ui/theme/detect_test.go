package theme

import (
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestQueryOSC11TimesOut(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer ptmx.Close()
	defer tty.Close()
	start := time.Now()
	_, ok := queryOSC11(tty, 100*time.Millisecond)
	if ok {
		t.Fatal("pty without a terminal reply must time out")
	}
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("timeout took %s", d)
	}
}
