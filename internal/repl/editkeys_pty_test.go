package repl

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/creack/pty"
)

func TestEditingKeysOnPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil && os.Getenv("KONSOLAI_PTY_CHILD") == "" {
		t.Skip("no /dev/ptmx")
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == "edit" {
		runEditChild()
	}
	for _, shell := range []bool{false, true} {
		name := "ai"
		if shell {
			name = "shell"
		}
		t.Run(name, func(t *testing.T) {
			for _, menu := range []bool{false, true} {
				mname := "closed"
				if menu {
					mname = "open"
				}
				t.Run(mname, func(t *testing.T) {
					v := startEdit(t, shell)
					exerciseEditing(t, v, menu)
				})
			}
		})
	}
}

func runEditChild() {
	rd, err := NewReadline(nil, func() string { return "FOOTER_MARK" }, nil, commands.Default())
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	if os.Getenv("KONSOLAI_EDIT_SHELL") == "1" {
		if s, ok := rd.(interface{ SetShellMode(bool) }); ok {
			s.SetShellMode(true)
		}
	}
	_, err = rd.ReadLine(context.Background(), "› ")
	if err != nil && err != ErrInterrupt && err != ErrEOF {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func startEdit(t *testing.T, shell bool) *ptyView {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEditingKeysOnPTY$")
	env := []string{"KONSOLAI_PTY_CHILD=edit", "COLUMNS=100", "INPUTRC=/dev/null"}
	if shell {
		env = append(env, "KONSOLAI_EDIT_SHELL=1")
	}
	cmd.Env = append(os.Environ(), env...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	})
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 100})
	v := startView(t, ptmx, 24, 100)
	v.expect("prompt", func(sc *screen) bool { return strings.Contains(sc.dump(), "›") })
	return v
}

// inputState is the text after the prompt and the cursor's column within it.
func inputState(sc *screen) (text string, cur int, ok bool) {
	row, _, ok := promptCursor(sc)
	if !ok || row < 0 || row >= len(sc.cell) {
		return "", 0, false
	}
	cells := sc.cell[row]
	start := -1
	for i, r := range cells {
		if r == '›' {
			start = i + 1
			if i+1 < len(cells) && cells[i+1] == ' ' {
				start = i + 2
			}
			break
		}
	}
	if start < 0 {
		return "", 0, false
	}
	end := start
	for i := len(cells) - 1; i >= start; i-- {
		if cells[i] != ' ' {
			end = i + 1
			break
		}
	}
	if sc.c > end {
		end = sc.c
	}
	if end > len(cells) {
		end = len(cells)
	}
	if end < start {
		end = start
	}
	return string(cells[start:end]), sc.c - start, true
}

func (v *ptyView) see(why string, ok func(*screen) bool) *screen {
	v.t.Helper()
	v.mu.Lock()
	before := v.mark
	v.mu.Unlock()
	return v.expect(why, func(sc *screen) bool {
		if len(v.raw()) <= before {
			return false
		}
		return ok(sc)
	})
}

func showLine(s string) string {
	return strings.NewReplacer("\n", "\\n", " ", "·").Replace(s)
}

func ntoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func exerciseEditing(t *testing.T, v *ptyView, menu bool) {
	t.Helper()
	prefix := ""
	if menu {
		prefix = "/"
	}
	clearLine := func() {
		t.Helper()
		v.write("\x01\x0b")
		v.see("clear", func(sc *screen) bool {
			text, _, ok := inputState(sc)
			return ok && strings.TrimSpace(text) == ""
		})
	}
	openMenu := func() {
		t.Helper()
		if !menu {
			return
		}
		v.typeKeys("/", func(sc *screen) bool { return strings.Contains(sc.dump(), "/help") })
	}
	prep := func(s string) {
		t.Helper()
		clearLine()
		openMenu()
		if s == "" {
			return
		}
		v.typeKeys(s, func(sc *screen) bool {
			text, _, ok := inputState(sc)
			return ok && text == prefix+s
		})
	}
	send := func(why, keys, want string, cur int) {
		t.Helper()
		v.write(keys)
		v.see(why, func(sc *screen) bool {
			text, got, ok := inputState(sc)
			if !ok || strings.ContainsAny(text, "[;~") || text != want {
				return false
			}
			return cur < 0 || got == cur
		})
		text, got, ok := inputState(v.shot())
		if !ok || strings.ContainsAny(text, "[;~") || text != want || (cur >= 0 && got != cur) {
			failScreen(t, why+" line "+showLine(text)+" cur "+ntoa(got)+" want "+showLine(want)+" cur "+ntoa(cur), v.shot(), v.raw())
		}
	}
	moved := func(why, keys, want string, minCur int) {
		t.Helper()
		send(why, keys, want, -1)
		_, got, ok := inputState(v.shot())
		if !ok || got < minCur {
			failScreen(t, why+" cursor "+ntoa(got), v.shot(), v.raw())
		}
	}

	if menu {
		clearLine()
		openMenu()
		send("left keeps menu", "\x1b[D", "/", 0)
		if !strings.Contains(v.shot().dump(), "/help") {
			failScreen(t, "left closed the slash menu", v.shot(), v.raw())
		}
		send("right keeps menu", "\x1b[C", "/", 1)
		if !strings.Contains(v.shot().dump(), "/help") {
			failScreen(t, "right closed the slash menu", v.shot(), v.raw())
		}
	}

	prep("abc")
	send("csi left", "\x1b[D", prefix+"abc", len(prefix)+2)
	send("csi left again", "\x1b[D", prefix+"abc", len(prefix)+1)
	send("insert X", "X", prefix+"aXbc", len(prefix)+2)

	prep("abc")
	send("ss3 left", "\x1bOD", prefix+"abc", len(prefix)+2)
	send("ss3 right", "\x1bOC", prefix+"abc", len(prefix)+3)
	send("csi left before right", "\x1b[D", prefix+"abc", len(prefix)+2)
	send("csi right", "\x1b[C", prefix+"abc", len(prefix)+3)

	wordAt := func(line string) int { return strings.LastIndex(line, "cd") }
	prep("ab cd")
	send("ctrl-left", "\x1b[1;5D", prefix+"ab cd", wordAt(prefix+"ab cd"))
	prep("ab cd")
	send("alt-left", "\x1b[1;3D", prefix+"ab cd", wordAt(prefix+"ab cd"))
	prep("ab cd")
	send("alt-b", "\x1bb", prefix+"ab cd", wordAt(prefix+"ab cd"))

	prep("ab cd")
	send("home csi", "\x1b[H", prefix+"ab cd", 0)
	moved("ctrl-right", "\x1b[1;5C", prefix+"ab cd", 1)
	prep("ab cd")
	send("home ss3", "\x1bOH", prefix+"ab cd", 0)
	moved("alt-right", "\x1b[1;3C", prefix+"ab cd", 1)
	prep("ab cd")
	send("home tilde", "\x1b[1~", prefix+"ab cd", 0)
	moved("alt-f", "\x1bf", prefix+"ab cd", 1)

	prep("abc")
	send("end csi", "\x1b[F", prefix+"abc", len(prefix)+3)
	send("home csi", "\x1b[H", prefix+"abc", 0)
	send("end ss3", "\x1bOF", prefix+"abc", len(prefix)+3)
	send("home ss3", "\x1bOH", prefix+"abc", 0)
	send("end tilde", "\x1b[4~", prefix+"abc", len(prefix)+3)
	send("home tilde", "\x1b[1~", prefix+"abc", 0)
	send("end 8", "\x1b[8~", prefix+"abc", len(prefix)+3)
	send("home 7", "\x1b[7~", prefix+"abc", 0)
	send("X after home", "X", "X"+prefix+"abc", 1)

	prep("abc")
	send("onto last", "\x1b[D", prefix+"abc", len(prefix)+2)
	send("delete", "\x1b[3~", prefix+"ab", len(prefix)+2)

	prep("abcd")
	send("home before ctrl-delete", "\x1b[H", prefix+"abcd", 0)
	v.write("\x1b[3;5~")
	v.see("ctrl-delete", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		if !ok || strings.ContainsAny(text, "[;~") || strings.Contains(text, "abcd") {
			return false
		}
		for _, r := range text {
			if !strings.ContainsRune(prefix+"abcd", r) {
				return false
			}
		}
		return len(text) < len(prefix+"abcd")
	})

	prep("abc")
	send("backspace", "\x7f", prefix+"ab", len(prefix)+2)

	prep("ab cd")
	send("ctrl-w", "\x17", prefix+"ab ", len(prefix)+3)

	prep("abc")
	for _, key := range []struct{ name, seq string }{
		{"pgup", "\x1b[5~"},
		{"pgdn", "\x1b[6~"},
		{"insert", "\x1b[2~"},
		{"f1 ss3", "\x1bOP"},
		{"f2 ss3", "\x1bOQ"},
		{"f3 ss3", "\x1bOR"},
		{"f4 ss3", "\x1bOS"},
		{"f1", "\x1b[11~"},
		{"f2", "\x1b[12~"},
		{"f3", "\x1b[13~"},
		{"f4", "\x1b[14~"},
		{"f5", "\x1b[15~"},
		{"f6", "\x1b[17~"},
		{"f7", "\x1b[18~"},
		{"f8", "\x1b[19~"},
		{"f9", "\x1b[20~"},
		{"f10", "\x1b[21~"},
		{"f11", "\x1b[23~"},
		{"f12", "\x1b[24~"},
	} {
		send(key.name, key.seq, prefix+"abc", len(prefix)+3)
	}

	prep("abc")
	v.write("\x1b")
	time.Sleep(10 * time.Millisecond)
	v.write("[D")
	v.see("split left", func(sc *screen) bool {
		text, got, ok := inputState(sc)
		return ok && text == prefix+"abc" && got == len(prefix)+2 && !strings.ContainsAny(text, "[;~")
	})

	prep("abc")
	v.write("\x1b\r")
	v.see("alt-enter", func(sc *screen) bool {
		row, _, ok := promptCursor(sc)
		if !ok || sc.r <= row {
			return false
		}
		text := strings.TrimRight(string(sc.cell[row]), " ")
		return strings.Contains(text, prefix+"abc") && !strings.ContainsAny(text, "[;")
	})
	v.write("\x7f")
	v.see("alt-enter backspace", func(sc *screen) bool {
		text, got, ok := inputState(sc)
		return ok && text == prefix+"abc" && got == len(prefix)+3 && !strings.ContainsAny(text, "[;~")
	})

	if menu {
		clearLine()
		v.typeKeys("/copy answer ", func(sc *screen) bool {
			d := sc.dump()
			return strings.Contains(d, "how many") || strings.Contains(d, "<n>")
		})
		v.typeKeys("12", func(sc *screen) bool {
			text, _, ok := inputState(sc)
			return ok && text == "/copy answer 12"
		})
		send("free-text left", "\x1b[D", "/copy answer 12", len("/copy answer 12")-1)
		send("free-text X", "X", "/copy answer 1X2", len("/copy answer 1X2")-1)
		v.write("\x1b")
		v.see("lone esc closes", func(sc *screen) bool {
			if strings.Contains(sc.dump(), "how many") || strings.Contains(sc.dump(), "<n>") {
				return false
			}
			text, _, ok := inputState(sc)
			return ok && !strings.ContainsAny(text, "[;~")
		})
		return
	}

	prep("abc")
	v.write("\x1b")
	v.see("lone esc", func(sc *screen) bool {
		text, _, ok := inputState(sc)
		return ok && strings.TrimSpace(text) == "" && !strings.ContainsAny(text, "[;~")
	})
}
