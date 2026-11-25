package repl

import "testing"

func TestRemoteReplyReAsksOnInvalid(t *testing.T) {
	cases := []struct {
		line  string
		yes   bool
		valid bool
	}{
		{"y", true, true}, {"YES", true, true},
		{"n", false, true}, {" No ", false, true},
		{"", false, false}, {"\r", false, false}, {"maybe", false, false}, {"yep", false, false},
	}
	for _, c := range cases {
		yes, valid := remoteReply(c.line)
		if yes != c.yes || valid != c.valid {
			t.Fatalf("remoteReply(%q) = (%v,%v), want (%v,%v)", c.line, yes, valid, c.yes, c.valid)
		}
	}
}
