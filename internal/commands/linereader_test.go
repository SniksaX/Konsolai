package commands

import (
	"context"
	"errors"
	"testing"
)

func TestReadLineFromEditor(t *testing.T) {
	saved := editorReader
	t.Cleanup(func() { editorReader = saved })
	editorReader = nil

	if _, err := ReadLineFromEditor(context.Background(), "› "); !errors.Is(err, ErrNoEditor) {
		t.Fatalf("no editor: %v", err)
	}
	SetEditorLineReader(func(_ context.Context, prompt string) (string, error) {
		if prompt != "ask › " {
			t.Fatalf("prompt %q", prompt)
		}
		return "typed line", nil
	})
	line, err := ReadLineFromEditor(context.Background(), "ask › ")
	if err != nil || line != "typed line" {
		t.Fatalf("%q %v", line, err)
	}
}
