package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

// The fake servers in this package are in-process (HTTP) or a re-exec of the
// test binary (stdio). No test talks to a real service.
func TestMain(m *testing.M) {
	if os.Getenv("KONSOLAI_MCP_GRANDCHILD") != "" {
		select {}
	}
	if mode := os.Getenv("KONSOLAI_MCP_FAKE"); mode != "" {
		runFakeChild(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeTool(name, description string, readOnly bool) *sdk.Tool {
	return &sdk.Tool{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{"type": "object"},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly},
	}
}

func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// newFakeServer builds a small SDK server for both the HTTP fake and the stdio
// child. mode selects the extra behavior a scenario needs.
func newFakeServer(mode string) *sdk.Server {
	_ = mode
	srv := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "0.0.1"}, nil)
	srv.AddTool(fakeTool("echo", "echo the input", true), func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return textResult("echo:" + string(req.Params.Arguments)), nil
	})
	srv.AddTool(fakeTool("envdump", "report the child environment", true), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		wd, _ := os.Getwd()
		payload, _ := json.Marshal(map[string]any{"Env": os.Environ(), "Cwd": wd})
		return textResult(string(payload)), nil
	})
	srv.AddTool(fakeTool("structured", "return structured content", true), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: "fields"}},
			StructuredContent: map[string]any{"answer": float64(42)},
		}, nil
	})
	srv.AddTool(fakeTool("fails", "a tool level error", false), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "boom"}}, IsError: true}, nil
	})
	srv.AddTool(fakeTool("big", "an oversized result", true), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return textResult(strings.Repeat("x", 200)), nil
	})
	srv.AddTool(fakeTool("secret", "echo the declared secret", true), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return textResult(os.Getenv("KONSOLAI_MCP_FAKE_SECRET")), nil
	})
	srv.AddTool(fakeTool("hang", "block until cancelled", false), func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	srv.AddTool(fakeTool("add-extra", "add a tool then answer", true), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		srv.AddTool(fakeTool("extra", "added at runtime", true), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return textResult("extra"), nil
		})
		return textResult("added"), nil
	})
	srv.AddTool(fakeTool("crash-once", "exit the process once", false), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		path := os.Getenv("KONSOLAI_MCP_FAKE_STATE")
		count := readCount(path)
		writeCount(path, count+1)
		if count == 0 {
			os.Exit(1)
		}
		return textResult("survived"), nil
	})
	srv.AddTool(fakeTool("crash-always", "exit the process every time", false), func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		os.Exit(1)
		return nil, nil
	})
	return srv
}

func readCount(path string) int {
	if path == "" {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func writeCount(path string, n int) {
	if path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o600)
	}
}

// runFakeChild runs inside the re-exec'd test binary.
func runFakeChild(mode string) {
	if mode == "malformed" {
		fmt.Fprintln(os.Stdout, "this is not a json-rpc frame")
		os.Exit(1)
	}
	if mode == "hang" {
		reportProcessGroup()
	}
	srv := newFakeServer(mode)
	if err := srv.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

// reportProcessGroup starts a grandchild in the same process group, records
// both pids, and tells the test the child is ready over stderr.
func reportProcessGroup() {
	grandchild := exec.Command(os.Args[0])
	grandchild.Env = append(os.Environ(), "KONSOLAI_MCP_GRANDCHILD=1")
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err == nil {
		grandchild.Stdout = devNull
		grandchild.Stderr = devNull
		defer devNull.Close()
	}
	if err := grandchild.Start(); err != nil {
		return
	}
	if path := os.Getenv("KONSOLAI_MCP_FAKE_PIDS"); path != "" {
		pgid, _ := syscall.Getpgid(0)
		_ = os.WriteFile(path, []byte(fmt.Sprintf("%d %d %d\n", os.Getpid(), grandchild.Process.Pid, pgid)), 0o600)
	}
	fmt.Fprintln(os.Stderr, "ready")
}

// newFakeHTTPServer runs the fake SDK server behind a streamable HTTP handler.
func newFakeHTTPServer(t *testing.T) (*sdk.Server, *httptest.Server) {
	t.Helper()
	srv := newFakeServer("http")
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return srv, ts
}

// stdioSpec points a spec at the test binary's fake child.
func stdioSpec(t *testing.T, mode string, extraEnv map[string]string) Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	env := map[string]string{"KONSOLAI_MCP_FAKE": mode}
	for key, value := range extraEnv {
		env[key] = value
	}
	return Spec{
		Name:      "fake",
		Transport: TransportStdio,
		Command:   exe,
		Env:       env,
		Enabled:   true,
		Timeout:   10 * time.Second,
	}
}

// childStderr captures a stdio child's stderr as a line channel.
func childStderr(t *testing.T) (*os.File, <-chan string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		pr.Close()
		pw.Close()
	})
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	return pw, lines
}

// waitLine waits for one stderr line.
func waitLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stderr closed while waiting for %q", want)
			}
			if line == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

// readPids reads the "<child> <grandchild> <pgid>" file.
func readPids(t *testing.T, path string) (int, int, int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pids: %v", err)
	}
	var child, grandchild, pgid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d %d %d", &child, &grandchild, &pgid); err != nil {
		t.Fatalf("parse pids %q: %v", b, err)
	}
	return child, grandchild, pgid
}

// processGone waits for a pidfd to report the exit. A pidfd becomes readable
// when the process exits, so this needs no scheduling sleep.
func processGone(t *testing.T, pid int) {
	t.Helper()
	if pid <= 0 {
		t.Fatalf("bad pid %d", pid)
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		t.Fatalf("pidfd open %d: %v", pid, err)
	}
	defer unix.Close(fd)
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 10000); err != nil {
		t.Fatalf("poll pid %d: %v", pid, err)
	}
	if fds[0].Revents&unix.POLLIN == 0 {
		t.Fatalf("process %d is still running", pid)
	}
}
