package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// stdioTerminateDuration lets a well-behaved child exit after its stdin closes
// before the SDK signals the process itself.
const stdioTerminateDuration = 500 * time.Millisecond

// transportBuild is one connected transport plus the handles needed to stop it.
type transportBuild struct {
	transport sdk.Transport
	// cancel tears the session down; for stdio it kills the whole process group.
	cancel func()
	// reap stops the transport and reaps the child. Only the connect-failure
	// path calls it; a live session is closed through ClientSession.Close.
	reap func()
	// pidFn reports the stdio child's process group id, or 0 before it starts.
	pidFn func() int
}

// buildTransport constructs the transport for one spec. Every call resolves the
// declared environment and headers again, so a value changed in the process
// environment applies on the next connect.
func (m *Manager) buildTransport(spec Spec) (transportBuild, error) {
	switch spec.Transport {
	case TransportStdio:
		return m.buildStdio(spec)
	case TransportHTTP:
		if err := plainHTTPHeaderError(spec.Name, parseURLOrNil(spec.URL), spec.Headers); err != nil {
			return transportBuild{}, err
		}
		return transportBuild{
			transport: &sdk.StreamableClientTransport{
				Endpoint:   spec.URL,
				HTTPClient: newHTTPClient(spec.Name, spec.URL, spec.Headers),
				// The manager owns the one allowed restart; the SDK must not
				// reconnect behind its back.
				MaxRetries: -1,
			},
		}, nil
	default:
		return transportBuild{}, fmt.Errorf("mcp: server %q: unknown transport %q", spec.Name, spec.Transport)
	}
}

// buildStdio starts one child with its own process group and an environment of
// PATH, HOME, and the declared entries only.
func (m *Manager) buildStdio(spec Spec) (transportBuild, error) {
	env, err := childEnv(spec.Env)
	if err != nil {
		return transportBuild{}, fmt.Errorf("mcp: server %q: %w", spec.Name, err)
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(sessionCtx, spec.Command, spec.Args...)
	if m.opts.ProjectDir != "" {
		cmd.Dir = m.opts.ProjectDir
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error { return killGroup(cmd) }
	if m.opts.Stderr != nil {
		cmd.Stderr = m.opts.Stderr
	}
	tr := &sdk.CommandTransport{Command: cmd, TerminateDuration: stdioTerminateDuration}
	build := transportBuild{
		transport: tr,
		cancel:    cancel,
		pidFn:     func() int { return childPID(cmd) },
	}
	build.reap = func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Wait()
		}
	}
	return build, nil
}

// killGroup kills the child and everything in its process group. A process
// that is already gone is not an error.
func killGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// childPID reports the started child's process group id, or 0 when the process
// has not started.
func childPID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}
