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
		tail = append(tail, n)
		if n >= '0' && n <= '9' {
			// SS3 with modifiers: ESC O 1;2 A.
			for {
				m, ok := next()
				if !ok {
					return tail, false
				}
				tail = append(tail, m)
				if m >= 0x40 && m <= 0x7e {
					return tail, false
				}
				if (m < '0' || m > '9') && m != ';' {
					return tail, false
				}
			}
		}
		return tail, false
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

// escapeArrow reports an Up or Down sequence, including the CSI, SS3 and
// modifier forms Konsole sends (ESC[A, ESC OA, ESC[1;5A, ESC O 1;2 A).
func escapeArrow(tail []byte) (byte, bool) {
	if len(tail) < 2 {
		return 0, false
	}
	if tail[0] != '[' && tail[0] != 'O' {
		return 0, false
	}
	body := tail[1:]
	final := body[len(body)-1]
	if final != 'A' && final != 'B' {
		return 0, false
	}
	for _, c := range body[:len(body)-1] {
		if (c < '0' || c > '9') && c != ';' {
			return 0, false
		}
	}
	return final, true
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
	case "[3~", "[3;2~", "[3;3~", "[3;4~":
		return "delete-char"
	case "[1~", "[7~", "[H", "OH",
		"[1;2H", "[1;3H", "[1;5H", "[1;7H", "O1;2H", "O1;3H", "O1;5H":
		return "beginning-of-line"
	case "[4~", "[8~", "[F", "OF",
		"[1;2F", "[1;3F", "[1;5F", "[1;7F", "O1;2F", "O1;3F", "O1;5F":
		return "end-of-line"
	case "[D", "OD", "[1;2D", "O1;2D":
		return "backward-char"
	case "[C", "OC", "[1;2C", "O1;2C":
		return "forward-char"
	case "[1;5D", "[1;3D", "O1;5D", "O1;3D", "b":
		return "backward-word"
	case "[1;5C", "[1;3C", "O1;5C", "O1;3C", "f":
		return "forward-word"
	case "[3;5~":
		return "kill-word"
	case "[13;2u", "[27;2;13~":
		// Shift+Enter: fixterms CSI-u and modifyOtherKeys encodings.
		return "insert-newline"
	case "\r", "\n":
		return "insert-newline"
	default:
		return ""
	}
}
