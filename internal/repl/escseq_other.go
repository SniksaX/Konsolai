//go:build !unix

package repl

import "time"

func readStdinByte(time.Duration) (byte, bool) { return 0, false }
