package repl

import (
	"strings"
	"time"

	"github.com/reeflective/readline/inputrc"
)

// readEscapeTail reads the bytes that follow Esc. A CSI sequence runs until
// its final byte. SS3 is Esc O plus one byte. Anything else is Alt+key, one
// byte. lone is true when the next byte does not arrive in time.
func readEscapeTail(next func() (byte, bool)) (tail []byte, lone bool) {
	b, ok := next()
	if !ok {
		return nil, true
	}
	tail = []byte{b}
	switch b {
	case '[':
		for {
			n, ok := next()
			if !ok {
				return tail, false
			}
			tail = append(tail, n)
			if n >= 0x40 && n <= 0x7e {
				return tail, false
			}
			if n < 0x20 || n > 0x3f {
				return tail, false
			}
		}
	case 'O':
		n, ok := next()
		if !ok {
			return tail, false
		}
		return append(tail, n), false
	default:
		return tail, false
	}
}

func (r *rlReader) nextEscByte(d time.Duration) (byte, bool) {
	if r != nil && r.sh != nil && r.sh.Keys != nil {
		if b, empty := r.sh.Keys.Pop(); !empty {
			return b, true
		}
	}
	return readStdinByte(d)
}

func arrowUp(tail []byte) bool {
	s := string(tail)
	return s == "[A" || s == "OA"
}

func arrowDown(tail []byte) bool {
	s := string(tail)
	return s == "[B" || s == "OB"
}

// actionFor returns the emacs binding for a full sequence, including the Esc
// byte. konsolai-esc is skipped so the reader cannot call itself.
func (r *rlReader) actionFor(raw string) string {
	if r == nil || r.sh == nil || r.sh.Config == nil || raw == "" {
		return ""
	}
	for _, name := range []string{"emacs", "emacs-standard"} {
		binds := r.sh.Config.Binds[name]
		for seq, bind := range binds {
			if bind.Macro || bind.Action == "" || bind.Action == "konsolai-esc" || bind.Action == "self-insert" {
				continue
			}
			if expandBind(seq) == raw {
				return bind.Action
			}
		}
	}
	return ""
}

func expandBind(seq string) string {
	var b strings.Builder
	for _, c := range seq {
		if inputrc.IsMeta(c) {
			b.WriteByte(0x1b)
			b.WriteRune(inputrc.Demeta(c))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// dropEsc is true for keys that must never reach the line or the history.
// Readline binds page up/down and insert; a prompt still ignores them.
func dropEsc(tail []byte) bool {
	switch string(tail) {
	case "[5~", "[6~", "[2~",
		"OP", "OQ", "OR", "OS",
		"[11~", "[12~", "[13~", "[14~", "[15~",
		"[17~", "[18~", "[19~", "[20~", "[21~", "[23~", "[24~":
		return true
	default:
		return false
	}
}

// fallbackEdit covers editing sequences the stock emacs map leaves unbound.
// Page, insert, and function keys are listed in dropEsc so they are never typed.
func fallbackEdit(tail []byte) string {
	switch string(tail) {
	case "[3~":
		return "delete-char"
	case "[1~", "[7~", "[H", "OH":
		return "beginning-of-line"
	case "[4~", "[8~", "[F", "OF":
		return "end-of-line"
	case "[D", "OD":
		return "backward-char"
	case "[C", "OC":
		return "forward-char"
	case "[1;5D", "[1;3D", "b":
		return "backward-word"
	case "[1;5C", "[1;3C", "f":
		return "forward-word"
	case "[3;5~":
		return "kill-word"
	case "\r", "\n":
		return "insert-newline"
	default:
		return ""
	}
}
