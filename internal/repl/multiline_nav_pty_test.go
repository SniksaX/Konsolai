package repl

import (
	"os"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
)

// Multi-line navigation on a real PTY: every key moves by the same rules, so a
// two-line input can be edited from either line and the submitted request shows
// exactly where the cursor was.

const ptyModeNavInput = "p0b-nav"

type navCase struct {
	name     string
	prefix   []string
	keys     []string
	want     string
	wantRows []string // screen row that must hold the cursor, when checked
	wantCol  int
	checkCol bool
}

func navScenario(t *testing.T, testName string) *ptyScenario {
	t.Helper()
	return startPTYScenario(t, testName, ptyModeNavInput, false, map[string]string{})
}

// primeTwoLines types "ab", a Shift+Enter newline, then "cd".
func primeTwoLines(t *testing.T, sc *ptyScenario) {
	t.Helper()
	sc.prompt(t)
	sc.v.typeKeys("ab", nil)
	sc.v.write("\x1b\r")
	sc.v.typeKeys("cd", nil)
}

func runNavCases(t *testing.T, testName string, cases []navCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNavInput {
				ptyChildRun()
				return
			}
			sc := navScenario(t, testName)
			primeTwoLines(t, sc)
			for _, key := range tc.prefix {
				sc.v.write(key)
			}
			for _, key := range tc.keys {
				sc.v.write(key)
			}
			if len(tc.wantRows) > 0 {
				sc.v.expect("cursor moved", func(screen *screen) bool {
					dump := screen.dump()
					row := lastRowOf(dump, tc.wantRows...)
					if row < 0 || screen.r != row {
						return false
					}
					return !tc.checkCol || screen.c == tc.wantCol
				})
			}
			sc.v.typeKeys("|", nil)
			sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 1 })
			if got := lastUserText(t, sc); got != tc.want {
				t.Fatalf("submitted %q, want %q\nscreen:\n%s", got, tc.want, sc.dump())
			}
		})
	}
}

// TestMultiLineVerticalNavigationPTY: Up and Down move between lines, in every
// CSI, SS3 and modifier form, and resume history only at the boundary.
func TestMultiLineVerticalNavigationPTY(t *testing.T) {
	cases := []navCase{
		{"up-csi", nil, []string{"\x1b[A"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"up-ss3", nil, []string{"\x1bOA"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"up-shift-csi", nil, []string{"\x1b[1;2A"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"up-alt-csi", nil, []string{"\x1b[1;3A"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"up-ctrl-csi", nil, []string{"\x1b[1;5A"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"up-shift-ss3", nil, []string{"\x1bO1;2A"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"up-ctrl-ss3", nil, []string{"\x1bO1;5A"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"down-csi", []string{"\x1b[H", "\x1b[A"}, []string{"\x1b[B"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"down-ss3", []string{"\x1b[H", "\x1b[A"}, []string{"\x1bOB"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"down-shift-csi", []string{"\x1b[H", "\x1b[A"}, []string{"\x1b[1;2B"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"down-ctrl-csi", []string{"\x1b[H", "\x1b[A"}, []string{"\x1b[1;5B"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"down-shift-ss3", []string{"\x1b[H", "\x1b[A"}, []string{"\x1bO1;2B"}, "ab\n|cd", []string{"  cd"}, 2, true},
		// Up/Down clamp to the shorter line instead of overshooting it.
		{"up-line-start", []string{"\x1b[H"}, []string{"\x1b[A"}, "|ab\ncd", []string{"› ab"}, 2, true},
		{"down-clamps", []string{"\x1b[A"}, []string{"\x1b[B"}, "ab\ncd|", nil, 0, false},
	}
	runNavCases(t, "TestMultiLineVerticalNavigationPTY", cases)
}

// TestMultiLineHistoryBoundaryPTY: Up on line one recalls history; Down on the
// last line walks forward again.
func TestMultiLineHistoryBoundaryPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNavInput {
		ptyChildRun()
		return
	}
	// One Up from line two moves a line and must not recall history.
	sc := navScenario(t, "TestMultiLineHistoryBoundaryPTY")
	sc.prompt(t)
	sc.v.typeKeys("first", nil)
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 1 })
	sc.v.expect("prompt", func(screen *screen) bool { return strings.Contains(screenPrompt(screen), "›") })
	sc.v.typeKeys("ab", nil)
	sc.v.write("\x1b\r")
	sc.v.typeKeys("cd", nil)
	sc.v.write("\x1b[A")
	sc.v.typeKeys("|", nil)
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 2 })
	if got := lastUserText(t, sc); got != "ab|\ncd" {
		t.Fatalf("one Up recalled history early: %q", got)
	}

	// Two Ups from line two recall the previous history entry.
	sc = navScenario(t, "TestMultiLineHistoryBoundaryPTY")
	sc.prompt(t)
	sc.v.typeKeys("first", nil)
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 1 })
	sc.v.expect("prompt", func(screen *screen) bool { return strings.Contains(screenPrompt(screen), "›") })
	sc.v.typeKeys("ab", nil)
	sc.v.write("\x1b\r")
	sc.v.typeKeys("cd", nil)
	sc.v.write("\x1b[A")
	sc.v.write("\x1b[A")
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 2 })
	if got := lastUserText(t, sc); got != "first" {
		t.Fatalf("Up on the first line recalled %q, want the history entry", got)
	}

	// Down on the last line walks forward to the line saved before the walk.
	sc = navScenario(t, "TestMultiLineHistoryBoundaryPTY")
	sc.prompt(t)
	sc.v.typeKeys("first", nil)
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 1 })
	sc.v.expect("prompt", func(screen *screen) bool { return strings.Contains(screenPrompt(screen), "›") })
	sc.v.typeKeys("ab", nil)
	sc.v.write("\x1b\r")
	sc.v.typeKeys("xy", nil)
	sc.v.write("\x1b[A")
	sc.v.write("\x1b[A")
	sc.v.write("\x1b[B")
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 2 })
	if got := lastUserText(t, sc); got != "ab\nxy" {
		t.Fatalf("Down on the last line did not walk forward: %q", got)
	}
}

// TestMultiLineClampPTY: a vertical move clamps to the shorter line.
func TestMultiLineClampPTY(t *testing.T) {
	cases := []navCase{
		{"up-clamps", nil, []string{"\x1b[A"}, "a|\nbcd", []string{"› a"}, 3, true},
		{"down-clamps", []string{"\x1b[A"}, []string{"\x1b[B"}, "a\nb|cd", []string{"  bcd"}, 3, true},
	}
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNavInput {
		ptyChildRun()
		return
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := navScenario(t, "TestMultiLineClampPTY")
			sc.prompt(t)
			sc.v.typeKeys("a", nil)
			sc.v.write("\x1b\r")
			sc.v.typeKeys("bcd", nil)
			for _, key := range tc.prefix {
				sc.v.write(key)
			}
			for _, key := range tc.keys {
				sc.v.write(key)
			}
			sc.v.expect("cursor moved", func(screen *screen) bool {
				dump := screen.dump()
				row := lastRowOf(dump, tc.wantRows...)
				return row >= 0 && screen.r == row && (!tc.checkCol || screen.c == tc.wantCol)
			})
			sc.v.typeKeys("|", nil)
			sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 1 })
			if got := lastUserText(t, sc); got != tc.want {
				t.Fatalf("submitted %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMultiLineHorizontalAndPairingPTY: Left/Right cross line boundaries,
// Home/End and Ctrl+A/Ctrl+E act per line, Backspace/Delete join lines.
func TestMultiLineHorizontalAndPairingPTY(t *testing.T) {
	cases := []navCase{
		{"left-mid", nil, []string{"\x1b[D"}, "ab\nc|d", []string{"  cd"}, 4, true},
		{"left-ss3", nil, []string{"\x1bOD"}, "ab\nc|d", nil, 0, false},
		{"left-shift", nil, []string{"\x1b[1;2D"}, "ab\nc|d", nil, 0, false},
		{"left-at-column-zero", []string{"\x1b[H"}, []string{"\x1b[D"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"left-at-column-zero-ss3", []string{"\x1b[H"}, []string{"\x1bO1;2D"}, "ab|\ncd", nil, 0, false},
		{"right-at-line-end", []string{"\x1b[H", "\x1b[A", "\x1b[F"}, []string{"\x1b[C"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"right-at-line-end-ss3", []string{"\x1b[H", "\x1b[A", "\x1b[F"}, []string{"\x1bOC"}, "ab\n|cd", nil, 0, false},
		{"right-at-buffer-end", nil, []string{"\x1b[C"}, "ab\ncd|", nil, 0, false},
		{"home-csi", nil, []string{"\x1b[H"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"home-ss3", nil, []string{"\x1bOH"}, "ab\n|cd", nil, 0, false},
		{"home-1~", nil, []string{"\x1b[1~"}, "ab\n|cd", nil, 0, false},
		{"home-7~", nil, []string{"\x1b[7~"}, "ab\n|cd", nil, 0, false},
		{"home-shift", nil, []string{"\x1b[1;2H"}, "ab\n|cd", nil, 0, false},
		{"home-ctrl", nil, []string{"\x1b[1;5H"}, "ab\n|cd", nil, 0, false},
		{"home-shift-ss3", nil, []string{"\x1bO1;2H"}, "ab\n|cd", nil, 0, false},
		{"ctrl-a", nil, []string{"\x01"}, "ab\n|cd", []string{"  cd"}, 2, true},
		{"end-csi", []string{"\x1b[H"}, []string{"\x1b[F"}, "ab\ncd|", []string{"  cd"}, 4, true},
		{"end-line-one", []string{"\x1b[A"}, []string{"\x1b[F"}, "ab|\ncd", []string{"› ab"}, 4, true},
		{"end-ss3", []string{"\x1b[H"}, []string{"\x1bOF"}, "ab\ncd|", nil, 0, false},
		{"end-4~", []string{"\x1b[H"}, []string{"\x1b[4~"}, "ab\ncd|", nil, 0, false},
		{"end-8~", []string{"\x1b[H"}, []string{"\x1b[8~"}, "ab\ncd|", nil, 0, false},
		{"end-shift", []string{"\x1b[H"}, []string{"\x1b[1;2F"}, "ab\ncd|", nil, 0, false},
		{"end-ctrl", []string{"\x1b[H"}, []string{"\x1b[1;5F"}, "ab\ncd|", nil, 0, false},
		{"end-shift-ss3", []string{"\x1b[H"}, []string{"\x1bO1;2F"}, "ab\ncd|", nil, 0, false},
		{"ctrl-e", []string{"\x1b[H"}, []string{"\x05"}, "ab\ncd|", nil, 0, false},
		{"backspace-mid", nil, []string{"\x7f"}, "ab\nc|", nil, 0, false},
		{"backspace-08", nil, []string{"\x08"}, "ab\nc|", nil, 0, false},
		{"backspace-at-column-zero", []string{"\x1b[H"}, []string{"\x7f"}, "ab|cd", []string{"› ab"}, 4, true},
		{"delete-mid", []string{"\x1b[D"}, []string{"\x1b[3~"}, "ab\nc|", nil, 0, false},
		{"delete-line-end", []string{"\x1b[A", "\x1b[F"}, []string{"\x1b[3~"}, "ab|cd", []string{"› ab"}, 4, true},
		{"delete-shift", []string{"\x1b[A", "\x1b[F"}, []string{"\x1b[3;2~"}, "ab|cd", nil, 0, false},
		{"delete-alt", []string{"\x1b[A", "\x1b[F"}, []string{"\x1b[3;3~"}, "ab|cd", nil, 0, false},
		{"delete-at-buffer-end", nil, []string{"\x1b[3~"}, "ab\ncd|", nil, 0, false},
	}
	runNavCases(t, "TestMultiLineHorizontalAndPairingPTY", cases)
}

// TestMultiLineTallInputPTY: the input is taller than the space below the
// prompt; the screen scrolls and the cursor lands on the last input line.
func TestMultiLineTallInputPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNavInput {
		ptyChildRun()
		return
	}
	sc := startPTYSized(t, "TestMultiLineTallInputPTY", ptyModeNavInput, false, map[string]string{}, 8, 60)
	sc.prompt(t)
	sc.run(t, "/help", screenHas("type / for the menu"))
	before := sc.v.shot()

	for i := 1; i <= 4; i++ {
		sc.v.write("l" + string(rune('0'+i)))
		if i < 4 {
			sc.v.write("\x1b\r")
		}
	}
	sc.v.expect("last input line", screenHas("l4"))
	shot := sc.v.shot()
	dump := sc.dump()
	rows := make([]int, 0, 4)
	for i := 1; i <= 4; i++ {
		row := lastRowOf(dump, "l"+string(rune('0'+i)))
		if row < 0 {
			t.Fatalf("input line l%d is not visible:\n%s", i, dump)
		}
		rows = append(rows, row)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i] != rows[i-1]+1 {
			t.Fatalf("input lines are not consecutive rows %v:\n%s", rows, dump)
		}
	}
	if rows[0] >= before.r {
		t.Fatalf("the window did not scroll up: first input row %d, prompt row %d:\n%s", rows[0], before.r, dump)
	}
	if shot.r != rows[len(rows)-1] {
		t.Fatalf("cursor row %d is not the last input row %d:\n%s", shot.r, rows[len(rows)-1], dump)
	}
	footer := lastRowOf(dump, "· main-model")
	if footer >= 0 && footer <= rows[len(rows)-1] {
		t.Fatalf("footer row %d is not below the input:\n%s", footer, dump)
	}
	sc.v.typeKeys("\r", func(screen *screen) bool { return chatCount(sc.model) >= 1 })
	if got := lastUserText(t, sc); got != "l1\nl2\nl3\nl4" {
		t.Fatalf("tall input submitted %q", got)
	}
}

// TestNavRules pins the cursor arithmetic without a PTY.
func TestNavRules(t *testing.T) {
	quietStdout(t)
	rd, err := NewReadline(nil, nil, nil, commands.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := rd.(*rlReader)
	set := func(text string, pos int) {
		r.sh.Line().Set([]rune(text)...)
		r.sh.Cursor().Set(pos)
	}
	pos := func() int { return r.sh.Cursor().Pos() }
	cases := []struct {
		name string
		text string
		at   int
		do   func()
		want int
		line string
	}{
		{"up", "ab\ncd", 5, r.navUp, 2, "ab\ncd"},
		{"up-clamp", "ab\ncd", 4, r.navUp, 1, "ab\ncd"},
		{"down", "ab\ncd", 0, r.navDown, 3, "ab\ncd"},
		{"down-clamp", "ab\ncd", 2, r.navDown, 5, "ab\ncd"},
		{"left", "ab\ncd", 4, r.navLeft, 3, "ab\ncd"},
		{"left-column-zero", "ab\ncd", 3, r.navLeft, 2, "ab\ncd"},
		{"right", "ab\ncd", 4, r.navRight, 5, "ab\ncd"},
		{"right-line-end", "ab\ncd", 2, r.navRight, 3, "ab\ncd"},
		{"home", "ab\ncd", 5, r.navHome, 3, "ab\ncd"},
		{"home-first", "ab\ncd", 1, r.navHome, 0, "ab\ncd"},
		{"end", "ab\ncd", 3, r.navEnd, 5, "ab\ncd"},
		{"end-first", "ab\ncd", 0, r.navEnd, 2, "ab\ncd"},
		{"backspace", "ab\ncd", 5, r.navBackspace, 4, "ab\nc"},
		{"backspace-join", "ab\ncd", 3, r.navBackspace, 2, "abcd"},
		{"delete", "ab\ncd", 4, r.navDelete, 4, "ab\nc"},
		{"delete-join", "ab\ncd", 2, r.navDelete, 2, "abcd"},
		{"delete-buffer-end", "ab\ncd", 5, r.navDelete, 5, "ab\ncd"},
		{"up-empty-line", "ab\n", 3, r.navUp, 0, "ab\n"},
		{"end-empty-line", "ab\n", 3, r.navEnd, 3, "ab\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set(tc.text, tc.at)
			tc.do()
			if got := pos(); got != tc.want {
				t.Fatalf("cursor %d, want %d", got, tc.want)
			}
			if got := string(*r.sh.Line()); got != tc.line {
				t.Fatalf("line %q, want %q", got, tc.line)
			}
		})
	}
}

func TestNavLineAt(t *testing.T) {
	cases := []struct {
		text string
		pos  int
		want navLine
	}{
		{"", 0, navLine{0, 0, 0, 1}},
		{"ab\ncd", 0, navLine{0, 0, 2, 2}},
		{"ab\ncd", 2, navLine{0, 0, 2, 2}},
		{"ab\ncd", 3, navLine{1, 3, 5, 2}},
		{"ab\ncd", 5, navLine{1, 3, 5, 2}},
		{"ab\n", 3, navLine{1, 3, 3, 2}},
		{"a\n\nb", 2, navLine{1, 2, 2, 3}},
	}
	for _, tc := range cases {
		if got := navLineAt([]rune(tc.text), tc.pos); got != tc.want {
			t.Errorf("navLineAt(%q,%d) = %+v, want %+v", tc.text, tc.pos, got, tc.want)
		}
	}
}

// TestEscapeArrowForms parses the CSI, SS3 and modifier Up/Down encodings.
func TestEscapeArrowForms(t *testing.T) {
	keys := map[string]byte{
		"[A": 'A', "OA": 'A', "[1;2A": 'A', "[1;5A": 'A', "O1;2A": 'A', "O1;5A": 'A',
		"[B": 'B', "OB": 'B', "[1;2B": 'B', "O1;2B": 'B',
	}
	for tail, want := range keys {
		if got, ok := escapeArrow([]byte(tail)); !ok || got != want {
			t.Errorf("escapeArrow(%q) = %q,%v want %q", tail, got, ok, want)
		}
	}
	for _, tail := range []string{"", "O", "[", "[C", "[13;2u", "[1;x", "[3~"} {
		if got, ok := escapeArrow([]byte(tail)); ok {
			t.Errorf("escapeArrow(%q) = %q, want no arrow", tail, got)
		}
	}
}

// TestMultiLineFallbackForms maps the Konsole modifier forms to the same
// actions as their unmodified keys.
func TestMultiLineFallbackForms(t *testing.T) {
	want := map[string]string{
		"[1;2D": "backward-char", "O1;2D": "backward-char",
		"[1;2C": "forward-char", "O1;2C": "forward-char",
		"[1;2H": "beginning-of-line", "O1;2H": "beginning-of-line",
		"[1;5H": "beginning-of-line",
		"[1;2F": "end-of-line", "O1;2F": "end-of-line",
		"[1;5F": "end-of-line",
		"[3;2~": "delete-char", "[3;3~": "delete-char", "[3;4~": "delete-char",
	}
	for tail, action := range want {
		if got := fallbackEdit([]byte(tail)); got != action {
			t.Errorf("fallbackEdit(%q) = %q, want %q", tail, got, action)
		}
	}
}
