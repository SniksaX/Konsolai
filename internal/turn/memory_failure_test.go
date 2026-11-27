package turn

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
)

// failingBackend fails every store call the guard makes; the answer must still
// be printed and the exit code must stay zero.
type failingBackend struct{ *store.Store }

func (failingBackend) NextSeq(context.Context, string) (int, error) {
	return 0, errors.New("injected: next seq")
}
func (failingBackend) AppendMessage(context.Context, string, int, store.Message) error {
	return errors.New("injected: append message")
}
func (failingBackend) TouchSession(context.Context, string) error {
	return errors.New("injected: touch session")
}
func (failingBackend) RecordAction(context.Context, store.ActionIn) error {
	return errors.New("injected: record action")
}
func (failingBackend) RecordFileChange(context.Context, store.FileChangeIn) error {
	return errors.New("injected: record file change")
}

func TestMemoryFailureKeepsTheAnswerAndExitCode(t *testing.T) {
	u := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if u == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	st, err := store.Open(context.Background(), u)
	if err != nil {
		t.Skipf("test database: %v", err)
	}
	t.Cleanup(st.Close)
	mem := memory.NewGuard(failingBackend{st}, func(error) {})
	res, out, errb, _ := runLeakTurn(t, leakKey(), "env", false, false, mem, Request{Text: "say hello"})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errb)
	}
	if !strings.Contains(out, "hello from the leak proof") {
		t.Fatalf("answer lost: %q", out)
	}
}
