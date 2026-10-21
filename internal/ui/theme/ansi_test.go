package theme

import "testing"

func TestScrollRegionKeepsCursor(t *testing.T) {
	if got := ScrollRegion(19, 40); got != "\x1b7\x1b[19;40r\x1b8" {
		t.Fatalf("%q", got)
	}
	for _, bad := range [][2]int{{0, 10}, {5, 4}} {
		if got := ScrollRegion(bad[0], bad[1]); got != "" {
			t.Fatalf("%v: %q", bad, got)
		}
	}
}
