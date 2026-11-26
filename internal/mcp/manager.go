package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager owns one lazy session per configured MCP server. A session is
// created on first use, restarted once after a crash, and closed by CloseAll.
// Calls from every server share one concurrency cap.
type Manager struct {
	opts    Options
	mu      sync.Mutex
	servers map[string]*serverState
	closed  bool
	sem     chan struct{}
}

type serverState struct {
	spec Spec

	// sessionMu guards the connection lifecycle and the restart state.
	sessionMu sync.Mutex
	session   *sdk.ClientSession
	cancel    context.CancelFunc
	restarted bool
	down      bool
	downErr   error
	pid       int

	// toolsMu guards the tool list cache only; the list-changed handler takes
	// it, so it must never be nested under sessionMu by the handler.
	toolsMu     sync.Mutex
	tools       []Tool
	toolsLoaded bool

	// closing tracks best-effort session shutdown, which must never hold a
	// call or a shutdown for a streaming transport's delete timeout.
	closing sync.WaitGroup
}

// NewManager validates the specs and builds a manager. Disabled specs are kept
// for status but never connected.
func NewManager(specs []Spec, opts Options) (*Manager, error) {
	opts = opts.withDefaults()
	m := &Manager{
		opts:    opts,
		servers: make(map[string]*serverState, len(specs)),
		sem:     make(chan struct{}, opts.MaxConcurrentCalls),
	}
	for _, raw := range specs {
		spec := raw.normalized()
		if err := spec.validate(); err != nil {
			return nil, err
		}
		if _, dup := m.servers[spec.Name]; dup {
			return nil, fmt.Errorf("mcp: duplicate server name %q", spec.Name)
		}
		m.servers[spec.Name] = &serverState{spec: spec}
	}
	return m, nil
}

// ServerDown reports whether a server was marked down after a second crash.
func (m *Manager) ServerDown(name string) bool {
	if m == nil {
		return false
	}
	st, ok := m.servers[name]
	if !ok {
		return false
	}
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	return st.down
}

// Tools returns the server's tool list, cached until a list-changed
// notification or a reconnect clears it.
func (m *Manager) Tools(ctx context.Context, name string) ([]Tool, error) {
	st, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	if cached, ok := m.cachedTools(st); ok {
		return cached, nil
	}
	var out []Tool
	err = m.run(ctx, st, func(opCtx context.Context, sess *sdk.ClientSession) error {
		list, listErr := m.listTools(opCtx, st, sess)
		if listErr != nil {
			return listErr
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, m.decorate(st, err)
	}
	m.storeTools(st, out)
	return copyTools(out), nil
}

// Call runs one tool and returns its raw result. A tool-level error stays in
// Result.IsError; a transport failure is retried once after a restart.
func (m *Manager) Call(ctx context.Context, name, tool string, args map[string]any) (Result, error) {
	st, err := m.lookup(name)
	if err != nil {
		return Result{}, err
	}
	if err := m.acquire(ctx); err != nil {
		return Result{}, err
	}
	defer m.release()

	timeout := m.timeoutFor(st.spec)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out Result
	err = m.run(callCtx, st, func(opCtx context.Context, sess *sdk.ClientSession) error {
		raw, callErr := sess.CallTool(opCtx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if callErr != nil {
			return callErr
		}
		result, convertErr := resultOf(st.spec.Name, tool, raw, m.opts.MaxResultBytes)
		if convertErr != nil {
			return nonRetryable(convertErr)
		}
		out = result
		return nil
	})
	if err != nil && callCtx.Err() != nil && ctx.Err() == nil {
		return Result{}, fmt.Errorf("mcp: server %q: tool %q timed out after %s: %w", name, tool, timeout, err)
	}
	if err != nil {
		return Result{}, m.decorate(st, err)
	}
	return out, nil
}

// CloseServer closes one server session, killing a stdio child's process group.
func (m *Manager) CloseServer(name string) error {
	if m == nil {
		return nil
	}
	st, ok := m.servers[name]
	if !ok {
		return unknownServerError(name)
	}
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	st.closeLocked()
	return nil
}

// CloseAll closes every session. It is safe to call during shutdown and more
// than once; the caller can use it from a Ctrl+C path.
func (m *Manager) CloseAll() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	states := make([]*serverState, 0, len(m.servers))
	for _, st := range m.servers {
		states = append(states, st)
	}
	m.mu.Unlock()
	for _, st := range states {
		st.sessionMu.Lock()
		st.closeLocked()
		st.sessionMu.Unlock()
	}
	return nil
}

func (m *Manager) lookup(name string) (*serverState, error) {
	if m == nil {
		return nil, errors.New("mcp: no manager")
	}
	m.mu.Lock()
	closed := m.closed
	st, ok := m.servers[name]
	m.mu.Unlock()
	if closed {
		return nil, errors.New("mcp: manager is closed")
	}
	if !ok {
		return nil, unknownServerError(name)
	}
	if !st.spec.Enabled {
		return nil, disabledError(name)
	}
	return st, nil
}

// Known reports the server state without connecting or listing: connected,
// down, disabled, or not connected. It never touches the transport, so a
// welcome repaint cannot spend a server round trip.
func (m *Manager) Known(name string) string {
	if m == nil {
		return "not connected"
	}
	m.mu.Lock()
	closed := m.closed
	st, ok := m.servers[name]
	m.mu.Unlock()
	if closed || !ok {
		return "not connected"
	}
	if !st.spec.Enabled {
		return "disabled"
	}
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	if st.down {
		return "down"
	}
	if st.session != nil {
		return "connected"
	}
	return "not connected"
}

// run connects if needed, runs op, and recovers from one crash. A caller
// cancellation tears the session down (and kills a stdio process group); a
// server that answers with a protocol error is not treated as a crash.
func (m *Manager) run(ctx context.Context, st *serverState, op func(context.Context, *sdk.ClientSession) error) error {
	sess, err := m.session(ctx, st)
	if err != nil {
		return err
	}
	err = op(ctx, sess)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		m.teardown(st)
		return err
	}
	if isPermanent(err) {
		return err
	}
	if restartErr := m.restart(ctx, st); restartErr != nil {
		return restartErr
	}
	sess, err = m.session(ctx, st)
	if err != nil {
		return err
	}
	err = op(ctx, sess)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		m.teardown(st)
		return err
	}
	if isPermanent(err) {
		return err
	}
	m.markDown(st, err)
	return m.downError(st)
}

// session returns the cached session, connecting when needed.
func (m *Manager) session(ctx context.Context, st *serverState) (*sdk.ClientSession, error) {
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	return m.connectLocked(ctx, st)
}

func (m *Manager) connectLocked(ctx context.Context, st *serverState) (*sdk.ClientSession, error) {
	if st.down {
		return nil, m.downErrorLocked(st)
	}
	if st.session != nil {
		return st.session, nil
	}
	build, err := m.buildTransport(st.spec)
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, m.timeoutFor(st.spec))
	defer cancel()
	client := sdk.NewClient(
		&sdk.Implementation{Name: m.opts.ClientName, Version: m.opts.ClientVersion},
		&sdk.ClientOptions{
			ToolListChangedHandler: func(context.Context, *sdk.ToolListChangedRequest) {
				m.invalidateTools(st)
			},
		},
	)
	sess, err := client.Connect(connectCtx, build.transport, nil)
	if err != nil {
		if build.reap != nil {
			build.reap()
		}
		return nil, fmt.Errorf("mcp: server %q: connect: %w", st.spec.Name, err)
	}
	st.session = sess
	st.cancel = build.cancel
	if build.pidFn != nil {
		st.pid = build.pidFn()
	}
	return sess, nil
}

// restart closes a crashed session and reconnects once per process.
func (m *Manager) restart(ctx context.Context, st *serverState) error {
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	if st.down {
		return m.downErrorLocked(st)
	}
	st.closeLocked()
	if st.restarted {
		m.markDownLocked(st, errors.New("crashed again after one restart"))
		return m.downErrorLocked(st)
	}
	st.restarted = true
	if _, err := m.connectLocked(ctx, st); err != nil {
		m.markDownLocked(st, err)
		return m.downErrorLocked(st)
	}
	return nil
}

// teardown drops a session without marking the server down, so the next call
// reconnects lazily.
func (m *Manager) teardown(st *serverState) {
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	st.closeLocked()
}

func (m *Manager) markDown(st *serverState, cause error) {
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	m.markDownLocked(st, cause)
}

func (m *Manager) markDownLocked(st *serverState, cause error) {
	if st.down {
		return
	}
	st.down = true
	st.downErr = cause
	st.closeLocked()
}

// closeLocked stops the session. It kills a stdio child's whole process group
// first. The session itself is closed in the background: a streaming HTTP
// transport can wait seconds for its delete request, and a tool call or
// shutdown must not block on that.
func (st *serverState) closeLocked() {
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	session := st.session
	st.session = nil
	st.pid = 0
	st.toolsMu.Lock()
	st.tools = nil
	st.toolsLoaded = false
	st.toolsMu.Unlock()
	if session != nil {
		st.closing.Add(1)
		go func() {
			defer st.closing.Done()
			_ = session.Close()
		}()
	}
}

func (m *Manager) downError(st *serverState) error {
	st.sessionMu.Lock()
	defer st.sessionMu.Unlock()
	return m.downErrorLocked(st)
}

func (m *Manager) downErrorLocked(st *serverState) error {
	if st.downErr == nil {
		return fmt.Errorf("%w: server %q", ErrServerDown, st.spec.Name)
	}
	return fmt.Errorf("%w: server %q: %v", ErrServerDown, st.spec.Name, st.downErr)
}

// decorate adds the server name to an error that does not have one yet.
func (m *Manager) decorate(st *serverState, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrServerDown) || errors.Is(err, ErrResultTooLarge) {
		return err
	}
	return fmt.Errorf("mcp: server %q: %w", st.spec.Name, err)
}

// listTools reads every page of tools/list.
func (m *Manager) listTools(ctx context.Context, st *serverState, sess *sdk.ClientSession) ([]Tool, error) {
	timeout := m.timeoutFor(st.spec)
	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out []Tool
	params := &sdk.ListToolsParams{}
	for {
		page, err := sess.ListTools(listCtx, params)
		if err != nil {
			return nil, err
		}
		tools, err := toolsOf(page)
		if err != nil {
			return nil, nonRetryable(err)
		}
		out = append(out, tools...)
		if page == nil || page.NextCursor == "" {
			return out, nil
		}
		params = &sdk.ListToolsParams{Cursor: page.NextCursor}
	}
}

func (m *Manager) cachedTools(st *serverState) ([]Tool, bool) {
	st.toolsMu.Lock()
	defer st.toolsMu.Unlock()
	if !st.toolsLoaded {
		return nil, false
	}
	return copyTools(st.tools), true
}

func (m *Manager) storeTools(st *serverState, tools []Tool) {
	st.toolsMu.Lock()
	defer st.toolsMu.Unlock()
	st.tools = copyTools(tools)
	st.toolsLoaded = true
}

func (m *Manager) invalidateTools(st *serverState) {
	st.toolsMu.Lock()
	st.tools = nil
	st.toolsLoaded = false
	st.toolsMu.Unlock()
	if m.opts.OnToolsChanged != nil {
		m.opts.OnToolsChanged(st.spec.Name)
	}
}

func copyTools(tools []Tool) []Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]Tool, len(tools))
	copy(out, tools)
	return out
}

func (m *Manager) timeoutFor(spec Spec) time.Duration {
	if spec.Timeout > 0 {
		return spec.Timeout
	}
	return m.opts.DefaultTimeout
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() { <-m.sem }

// isPermanent reports an error that a restart cannot fix: a conversion failure
// or a protocol error the server itself answered.
func isPermanent(err error) bool {
	if isNonRetryable(err) {
		return true
	}
	var rpcErr *jsonrpc.Error
	return errors.As(err, &rpcErr)
}
