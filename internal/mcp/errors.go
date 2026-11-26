package mcp

import (
	"errors"
	"fmt"
)

var (
	// ErrServerDown is returned after a server crashed twice: the manager
	// restarts it once per process, then leaves it down.
	ErrServerDown = errors.New("mcp: server is down")
	// ErrResultTooLarge is returned when a tool result passes the raw size cap.
	ErrResultTooLarge = errors.New("mcp: tool result is too large")
)

// errServerResult marks a response problem that a restart cannot fix: the
// server answered, so the session is healthy.
type errServerResult struct {
	err error
}

func (e errServerResult) Error() string { return e.err.Error() }
func (e errServerResult) Unwrap() error { return e.err }

func nonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return errServerResult{err: err}
}

func isNonRetryable(err error) bool {
	var target errServerResult
	return errors.As(err, &target)
}

// disabledError names a disabled server.
func disabledError(name string) error {
	return fmt.Errorf("mcp: server %q is disabled", name)
}

// unknownServerError names a server the manager does not know.
func unknownServerError(name string) error {
	return fmt.Errorf("mcp: server %q is not configured", name)
}
