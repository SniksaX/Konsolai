package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// mcpPrivatePrompt asks once per process before a private MCP server's results
// reach a remote main model. The runtime remembers the answer; --yes skips the
// hook entirely, and a non-interactive run refuses it.
func mcpPrivatePrompt(server string) (bool, error) {
	return confirmMCPPrivate(server, stdinIsTTY(), os.Stdin, os.Stderr)
}

// confirmMCPPrivate reads the y/n answer for one private MCP server. The prompt
// names the server only; no configured env value, header value or command line
// enters it. A non-interactive run refuses, and EOF or Ctrl+C count as n.
func confirmMCPPrivate(server string, tty bool, in io.Reader, out io.Writer) (bool, error) {
	if !tty {
		return false, nil
	}
	fmt.Fprintf(out, "this sends MCP results from %s to the remote model [y/N]: ", server)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
