// Package mcp is the client half of the MCP integration: it owns one lazy
// session per configured server, keeps the tool list fresh, and returns raw
// tool results for the bridge to wrap. It deliberately has no dependency on
// the config package: the integration maps a profile to Spec values.
package mcp

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Transport names accepted by Spec.Transport.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

const (
	// DefaultTimeout bounds one connect, tool list, or tool call.
	DefaultTimeout = 30 * time.Second
	// DefaultConcurrentCalls is the process-wide cap on in-flight tool calls.
	DefaultConcurrentCalls = 4
	// DefaultMaxResultBytes is the raw result size a call may return.
	DefaultMaxResultBytes = 8 << 20
	// DefaultClientName identifies Konsolai to a server.
	DefaultClientName = "konsolai"
	// DefaultClientVersion is used when the caller has no version string.
	DefaultClientVersion = "dev"
)

// Spec describes one MCP server. It is the package's own struct so the
// config package stays free to map its profile later.
type Spec struct {
	// Name is the configured server name. It is the key for every call.
	Name string
	// Transport is TransportStdio or TransportHTTP.
	Transport string
	// Command is the executable for a stdio server.
	Command string
	// Args are the stdio server arguments.
	Args []string
	// Env is the declared child environment. Values may reference ${NAME}.
	Env map[string]string
	// URL is the streamable HTTP endpoint.
	URL string
	// Headers are request headers for an HTTP server. Values may reference
	// ${NAME} and are resolved at request time.
	Headers map[string]string
	// Trust is the configured trust label. It is carried for the caller's
	// policy and is not enforced here.
	Trust string
	// Enabled false keeps the server out of the manager's active set.
	Enabled bool
	// Private marks a server the caller keeps out of shared output.
	Private bool
	// Timeout bounds one connect, list, or call. Zero uses Options.DefaultTimeout.
	Timeout time.Duration
	// ReadOnlyTools names tools the caller treats as read only in addition to
	// the server's readOnlyHint annotation.
	ReadOnlyTools []string
}

// normalized lowercases the transport and trims names.
func (s Spec) normalized() Spec {
	s.Name = strings.TrimSpace(s.Name)
	s.Transport = strings.ToLower(strings.TrimSpace(s.Transport))
	s.Command = strings.TrimSpace(s.Command)
	s.URL = strings.TrimSpace(s.URL)
	return s
}

// validate checks a normalized spec. A bad spec is a caller bug, so the
// message names the field and keeps every value that may be a secret out.
func (s Spec) validate() error {
	if s.Name == "" {
		return errors.New("mcp: a server spec needs a name")
	}
	if s.Timeout < 0 {
		return fmt.Errorf("mcp: server %q: timeout must be >= 0", s.Name)
	}
	switch s.Transport {
	case TransportStdio:
		if s.Command == "" {
			return fmt.Errorf("mcp: server %q: stdio needs a command", s.Name)
		}
		if s.URL != "" {
			return fmt.Errorf("mcp: server %q: stdio has a URL", s.Name)
		}
		if len(s.Headers) > 0 {
			return fmt.Errorf("mcp: server %q: stdio has headers", s.Name)
		}
	case TransportHTTP:
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("mcp: server %q: http has a command, args, or env", s.Name)
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("mcp: server %q: url must be http or https", s.Name)
		}
	default:
		return fmt.Errorf("mcp: server %q: unknown transport %q", s.Name, s.Transport)
	}
	for name := range s.Env {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("mcp: server %q: an env entry has an empty name", s.Name)
		}
	}
	for name := range s.Headers {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("mcp: server %q: a header has an empty name", s.Name)
		}
	}
	return nil
}

// Options configures a Manager. Zero values keep the package defaults.
type Options struct {
	// ProjectDir is the working directory of every stdio child.
	ProjectDir string
	// DefaultTimeout is used when a Spec has no Timeout.
	DefaultTimeout time.Duration
	// MaxConcurrentCalls caps in-flight tool calls across all servers.
	MaxConcurrentCalls int
	// MaxResultBytes bounds the raw result a tool call may return.
	MaxResultBytes int
	// ClientName and ClientVersion identify this client to a server.
	ClientName    string
	ClientVersion string
	// OnToolsChanged runs after a server reports that its tool list changed.
	// The next Tools call reads it again.
	OnToolsChanged func(server string)
	// Stderr receives a stdio child's stderr when set.
	Stderr io.Writer
}

func (o Options) withDefaults() Options {
	if o.DefaultTimeout <= 0 {
		o.DefaultTimeout = DefaultTimeout
	}
	if o.MaxConcurrentCalls <= 0 {
		o.MaxConcurrentCalls = DefaultConcurrentCalls
	}
	if o.MaxResultBytes <= 0 {
		o.MaxResultBytes = DefaultMaxResultBytes
	}
	if strings.TrimSpace(o.ClientName) == "" {
		o.ClientName = DefaultClientName
	}
	if strings.TrimSpace(o.ClientVersion) == "" {
		o.ClientVersion = DefaultClientVersion
	}
	return o
}

// Names returns the configured server names in sorted order, enabled or not.
func (m *Manager) Names() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.servers))
	for name := range m.servers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ServerEnabled reports whether the manager will talk to a server.
func (m *Manager) ServerEnabled(name string) bool {
	if m == nil {
		return false
	}
	st, ok := m.servers[name]
	return ok && st.spec.Enabled
}
