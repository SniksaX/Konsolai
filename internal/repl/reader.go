package repl

import (
	"context"
	"errors"
)

var (
	ErrInterrupt = errors.New("repl: interrupt")
	ErrEOF       = errors.New("repl: eof")
)

type LineReader interface {
	ReadLine(ctx context.Context, prompt string) (string, error)
	Close() error
}
