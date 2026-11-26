package repl

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
)

// Newline input scenarios: Shift+Enter reaches the editor as several terminal
// sequences; each one must insert a newline in place while plain Enter submits.

const ptyModeNewline = "p0b-newline"

var newlineSequences = []struct{ name, seq string }{
	{"LF", "\n"},
	{"ALT_ENTER_ESC_CR", "\x1b\r"},
	{"CSI_U_13_2", "\x1b[13;2u"},
	{"MODIFY_OTHER_KEYS_27_2_13", "\x1b[27;2;13~"},
}

func newlineScenario(t *testing.T, testName string) *ptyScenario {
	t.Helper()
	return startPTYScenario(t, testName, ptyModeNewline, false, map[string]string{})
}

// chatUserMessages returns the user messages of the last non-router chat request.
func chatUserMessages(t *testing.T, sc *ptyScenario) []string {
	t.Helper()
	var last string
	for _, body := range sc.model.requestBodies() {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			continue
		}
		if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "Classify the user's request") {
			continue
		}
		last = body
	}
	if last == "" {
		t.Fatal("the fake provider saw no chat request")
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(last), &req); err != nil {
		t.Fatalf("decode chat request: %v", err)
	}
	var out []string
	for _, m := range req.Messages {
		if m.Role == "user" {
			out = append(out, m.Content)
		}
	}
	return out
}

func lastUserText(t *testing.T, sc *ptyScenario) string {
	t.Helper()
	msgs := chatUserMessages(t, sc)
	if len(msgs) == 0 {
		t.Fatal("the fake provider saw no user message")
	}
	return msgs[len(msgs)-1]
}

// lastRowOf returns the last screen row containing every part, or -1.
func lastRowOf(dump string, parts ...string) int {
	rows := strings.Split(dump, "\n")
	for i := len(rows) - 1; i >= 0; i-- {
		ok := true
		for _, part := range parts {
			if !strings.Contains(rows[i], part) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// TestNewlineSequencesInsertNewlineOnPTY drives each terminal encoding of
// Shift+Enter: the submitted request must contain the inserted newline.
func TestNewlineSequencesInsertNewlineOnPTY(t *testing.T) {
	for _, tc := range newlineSequences {
		t.Run(tc.name, func(t *testing.T) {
			if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNewline {
				ptyChildRun()
				return
			}
			sc := newlineScenario(t, "TestNewlineSequencesInsertNewlineOnPTY")
			sc.prompt(t)
			sc.v.typeKeys("one", nil)
			sc.v.write(tc.seq)
			sc.v.typeKeys("two", nil)

			dump := sc.dump()
			first := lastRowOf(dump, "› one")
			second := lastRowOf(dump, "two")
			if first < 0 || second < 0 || second <= first {
				t.Fatalf("the prompt did not grow over two lines:\n%s", dump)
			}
			if footer := lastRowOf(dump, "· main-model"); footer >= 0 && footer <= second {
				t.Fatalf("the footer did not move below the second input line (footer %d, input %d):\n%s", footer, second, dump)
			}

			sc.v.typeKeys("\r", screenHas("done"))
			if got := lastUserText(t, sc); got != "one\ntwo" {
				t.Fatalf("submitted %q, want %q", got, "one\ntwo")
			}
		})
	}
}

// TestPlainEnterSubmitsWithoutNewlineOnPTY keeps plain Enter as submit.
func TestPlainEnterSubmitsWithoutNewlineOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNewline {
		ptyChildRun()
		return
	}
	sc := newlineScenario(t, "TestPlainEnterSubmitsWithoutNewlineOnPTY")
	sc.prompt(t)
	sc.v.typeKeys("one", nil)
	sc.v.typeKeys("\r", screenHas("done"))
	if got := lastUserText(t, sc); got != "one" {
		t.Fatalf("plain Enter submitted %q, want %q", got, "one")
	}
}

// TestMultiLinePromptFooterAndNavigationOnPTY: the prompt and footer grow with
// the input, and Home/End/Up work per line.
func TestMultiLinePromptFooterAndNavigationOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNewline {
		ptyChildRun()
		return
	}
	sc := newlineScenario(t, "TestMultiLinePromptFooterAndNavigationOnPTY")
	sc.prompt(t)
	sc.v.typeKeys("ab", nil)
	beforeFooter := lastRowOf(sc.dump(), "ctx")
	sc.v.write("\x1b\r")
	sc.v.typeKeys("cd", nil)

	dump := sc.dump()
	first := lastRowOf(dump, "› ab")
	second := lastRowOf(dump, "cd")
	afterFooter := lastRowOf(dump, "· main-model")
	if first < 0 || second <= first {
		t.Fatalf("input did not grow over two lines:\n%s", dump)
	}
	if afterFooter <= second {
		t.Fatalf("footer row %d is not below the second input row %d:\n%s", afterFooter, second, dump)
	}
	if beforeFooter >= 0 && afterFooter != beforeFooter+1 {
		t.Fatalf("footer moved from %d to %d, want one row down:\n%s", beforeFooter, afterFooter, dump)
	}
	shot := sc.v.shot()
	if shot.r != second {
		t.Fatalf("cursor row %d is not the second input row %d:\n%s", shot.r, second, dump)
	}

	sc.v.write("\x1b[H")
	sc.v.typeKeys("X", nil)
	sc.v.write("\x1b[F")
	sc.v.typeKeys("Y", nil)
	sc.v.write("\x1b[A")
	sc.v.write("\x1b[F")
	sc.v.typeKeys("Z", nil)
	sc.v.write("\x1b[B")
	sc.v.typeKeys("W", nil)
	sc.v.typeKeys("\r", screenHas("done"))
	if got := lastUserText(t, sc); got != "abZ\nXcdWY" {
		t.Fatalf("navigation submitted %q, want %q", got, "abZ\nXcdWY")
	}
}

// TestPasteWithNewlinesChipsOnPTY: a multi-line paste keeps the chip and the
// submitted request expands to the raw text.
func TestPasteWithNewlinesChipsOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNewline {
		ptyChildRun()
		return
	}
	sc := newlineScenario(t, "TestPasteWithNewlinesChipsOnPTY")
	sc.prompt(t)
	var body strings.Builder
	for i := 1; i <= 11; i++ {
		if i > 1 {
			body.WriteByte('\n')
		}
		body.WriteString("pasted line ")
		body.WriteString(string(rune('0' + i%10)))
	}
	pasted := body.String()
	sc.v.write("\x1b[200~" + pasted + "\x1b[201~")
	sc.v.expect("paste chip", screenHas("[pasted #1"))
	if strings.Contains(sc.dump(), "pasted line 1") {
		t.Fatalf("the multi-line paste was not chipped:\n%s", sc.dump())
	}
	sc.v.typeKeys("\r", screenHas("done"))
	if got := lastUserText(t, sc); got != pasted {
		t.Fatalf("the chip did not expand to the pasted text: %q", got)
	}
}

// TestNewlineBindingsOnEditor pins the editor bindings without a PTY.
func TestNewlineBindingsOnEditor(t *testing.T) {
	quietStdout(t)
	rd, err := NewReadline(nil, nil, nil, commands.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := rd.(*rlReader)
	if got := r.actionFor("\n"); got != "insert-newline" {
		t.Fatalf("LF binding %q", got)
	}
	if got := r.actionFor("\r"); got != "konsolai-enter" {
		t.Fatalf("CR binding %q", got)
	}
	for _, tail := range []string{"[13;2u", "[27;2;13~", "\r", "\n"} {
		if got := fallbackEdit([]byte(tail)); got != "insert-newline" {
			t.Fatalf("fallback %q => %q", tail, got)
		}
	}
	// Esc-then-key behaviour is unchanged.
	if got := r.actionFor("\x1bb"); got != "backward-word" {
		t.Fatalf("alt-b binding %q", got)
	}
	if got := fallbackEdit([]byte("b")); got != "backward-word" {
		t.Fatalf("alt-b fallback %q", got)
	}
	if got := fallbackEdit([]byte("[D")); got != "backward-char" {
		t.Fatalf("left arrow fallback %q", got)
	}
}
