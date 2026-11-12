package commands

import (
	"context"
	"errors"
)

// LineReader reads one line through the REPL's own editor: history, arrow keys
// and paste all work, unlike a plain read of stdin.
type LineReader func(ctx context.Context, prompt string) (string, error)

// ErrNoEditor is returned when no REPL is running to provide an editor.
var ErrNoEditor = errors.New("commands: no editor line reader")

var editorReader LineReader

// SetEditorLineReader installs the REPL's reader. The REPL calls this once.
func SetEditorLineReader(r LineReader) { editorReader = r }

// ReadLineFromEditor reads one line with the editor, or returns ErrNoEditor.
func ReadLineFromEditor(ctx context.Context, prompt string) (string, error) {
	if editorReader == nil {
		return "", ErrNoEditor
	}
	return editorReader(ctx, prompt)
}
