package theme

import "testing"

func TestUnitWidthGlyphs(t *testing.T) {
	for _, r := range []rune{'◆', '◇', '●', '○', '▸', '✓', '✗', '•', '›'} {
		if DisplayWidth(string(r)) != 1 {
			t.Fatalf("%q width %d", r, DisplayWidth(string(r)))
		}
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("hello world", 20) != "hello world" {
		t.Fatal(Truncate("hello world", 20))
	}
	got := Truncate("hello world", 8)
	if DisplayWidth(got) > 8 || got != "hello..." {
		t.Fatalf("%q", got)
	}
}
