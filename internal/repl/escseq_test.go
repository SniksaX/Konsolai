package repl

import (
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
)

func TestReadEscapeTail(t *testing.T) {
	next := func(b ...byte) func() (byte, bool) {
		i := 0
		return func() (byte, bool) {
			if i >= len(b) {
				return 0, false
			}
			c := b[i]
			i++
			return c, true
		}
	}
	seq := func(s string) []byte { return []byte(s) }
	cases := []struct {
		name string
		in   []byte
		tail string
		lone bool
	}{
		{name: "lone", lone: true},
		{name: "left", in: seq("[D"), tail: "[D"},
		{name: "ss3", in: seq("OC"), tail: "OC"},
		{name: "ctrl-left", in: seq("[1;5D"), tail: "[1;5D"},
		{name: "delete", in: seq("[3~"), tail: "[3~"},
		{name: "alt-b", in: seq("b"), tail: "b"},
		{name: "alt-enter", in: []byte{'\r'}, tail: "\r"},
		{name: "page", in: seq("[5~"), tail: "[5~"},
		{name: "f1", in: seq("OP"), tail: "OP"},
		{name: "incomplete", in: seq("["), tail: "["},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, lone := readEscapeTail(next(tc.in...))
			if lone != tc.lone || string(got) != tc.tail {
				t.Fatalf("tail %q lone %v", got, lone)
			}
		})
	}
}

func TestEscapeActionForEditingKeys(t *testing.T) {
	quietStdout(t)
	rd, err := NewReadline(nil, nil, nil, commands.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := rd.(*rlReader)
	want := map[string]string{
		"\x1b[D":    "backward-char",
		"\x1b[C":    "forward-char",
		"\x1bOD":    "backward-char",
		"\x1bOC":    "forward-char",
		"\x1b[1;5D": "backward-word",
		"\x1b[1;5C": "forward-word",
		"\x1b[1;3D": "backward-word",
		"\x1b[1;3C": "forward-word",
		"\x1b[H":    "beginning-of-line",
		"\x1b[F":    "end-of-line",
		"\x1bOH":    "beginning-of-line",
		"\x1bOF":    "end-of-line",
		"\x1b[1~":   "beginning-of-line",
		"\x1b[4~":   "end-of-line",
		"\x1b[3~":   "delete-char",
		"\x1b[3;5~": "kill-word",
		"\x1bb":     "backward-word",
		"\x1bf":     "forward-word",
		"\x1b\r":    "insert-newline",
	}
	for seq, action := range want {
		if got := r.actionFor(seq); got != action {
			t.Errorf("%q => %q want %s", seq, got, action)
		}
		if fb := fallbackEdit([]byte(seq[1:])); fb != action {
			t.Errorf("fallback %q => %q want %s", seq, fb, action)
		}
	}
	for _, seq := range []string{"\x1b[5~", "\x1b[6~", "\x1b[2~", "\x1bOP", "\x1b[15~", "\x1b[24~"} {
		if !dropEsc([]byte(seq[1:])) {
			t.Errorf("%q should be ignored", seq)
		}
		if fb := fallbackEdit([]byte(seq[1:])); fb != "" {
			t.Errorf("fallback %q => %q", seq, fb)
		}
	}
}
