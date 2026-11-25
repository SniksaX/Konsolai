package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
)

// lookupIP is a hook so tests can classify hosts without DNS.
var lookupIP = net.LookupIP

var remoteMu sync.Mutex
var remoteCache = map[string]bool{}

// ModelRemote reports whether a model's endpoint is outside loopback and the
// private address ranges. Unresolvable hosts count as remote.
func (c Config) ModelRemote(name string) bool {
	m, ok := c.Models[name]
	return ok && m.Remote()
}

// Remote reports whether this model points at a remote host.
func (m Model) Remote() bool { return URLIsRemote(m.Endpoint) }

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("config: not an HTTP endpoint: %q", raw)
	}
	return u, nil
}

// URLHost is the endpoint host used in remote messages.
func URLHost(raw string) string {
	u, err := parseHTTPURL(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return u.Host
}

// URLIsRemote classifies a URL's host. Empty or unparsable input is not remote.
func URLIsRemote(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := parseHTTPURL(raw)
	if err != nil {
		return false
	}
	return HostRemote(u.Hostname())
}

// URLResolvesRemote is the stricter check for embeddings: only a host that
// resolves to a public address counts. An unresolved host is allowed because
// a container name may not resolve at validation time.
func URLResolvesRemote(raw string) bool {
	u, err := parseHTTPURL(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !privateIP(ip)
	}
	ips, err := lookupIP(h)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !privateIP(ip) {
			return true
		}
	}
	return false
}

// HostRemote classifies a host: loopback and private addresses stay local,
// everything else is remote.
func HostRemote(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return !privateIP(ip)
	}
	remoteMu.Lock()
	if v, ok := remoteCache[h]; ok {
		remoteMu.Unlock()
		return v
	}
	remoteMu.Unlock()
	remote := true
	if ips, err := lookupIP(h); err == nil && len(ips) > 0 {
		remote = false
		for _, ip := range ips {
			if !privateIP(ip) {
				remote = true
				break
			}
		}
	}
	remoteMu.Lock()
	remoteCache[h] = remote
	remoteMu.Unlock()
	return remote
}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// TokensField is the request field that carries the output cap.
func (m Model) TokensField() string {
	if f := strings.TrimSpace(m.MaxTokensField); f != "" {
		return f
	}
	return "max_tokens"
}

// SendsTemperature is true unless the model explicitly disables it.
func (m Model) SendsTemperature() bool {
	return m.SendTemperature == nil || *m.SendTemperature
}

// APIKeyVariable is the environment variable named by the model, if any.
func (m Model) APIKeyVariable() string { return strings.TrimSpace(m.APIKeyEnv) }

// RemoteRoleAllowed reports whether privacy.remote_roles lets this role use a
// remote model. An empty list means no role is restricted.
func (c Config) RemoteRoleAllowed(role string) bool {
	if len(c.Privacy.RemoteRoles) == 0 {
		return true
	}
	for _, r := range c.Privacy.RemoteRoles {
		if strings.EqualFold(strings.TrimSpace(r), role) {
			return true
		}
	}
	return false
}
