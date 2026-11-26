package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// maxRedirects bounds a same-host redirect chain.
const maxRedirects = 5

// parseURLOrNil parses an endpoint for the up-front header check.
func parseURLOrNil(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

// newHTTPClient builds the client for one HTTP server. The transport resolves
// and adds the configured headers at request time, and the redirect policy
// refuses a cross-host hop or an https to http downgrade.
func newHTTPClient(server, endpoint string, headers map[string]string) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Transport: &headerTransport{
			base:    base,
			server:  server,
			headers: headers,
		},
		CheckRedirect: redirectPolicy(server),
	}
}

type headerTransport struct {
	base    http.RoundTripper
	server  string
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.headers) == 0 {
		resp, err := t.base.RoundTrip(req)
		return resp, err
	}
	if err := plainHTTPHeaderError(t.server, req.URL, t.headers); err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	secrets := make([]string, 0, len(t.headers))
	for name, raw := range t.headers {
		value, err := resolveValue(raw)
		if err != nil {
			return nil, fmt.Errorf("mcp: server %q: header %q: %w", t.server, name, err)
		}
		if value != "" {
			secrets = append(secrets, value)
		}
		clone.Header.Set(name, value)
	}
	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, maskError(err, secrets)
	}
	return resp, nil
}

// plainHTTPHeaderError refuses to send any configured header over plain HTTP
// to a host that is not local. The error names the header names, never a value.
func plainHTTPHeaderError(server string, u *url.URL, headers map[string]string) error {
	if u == nil || u.Scheme != "http" || len(headers) == 0 {
		return nil
	}
	host := u.Hostname()
	if isLocalHost(host) {
		return nil
	}
	return fmt.Errorf("mcp: server %q: refusing to send %s over plain HTTP to non-local host %q", server, strings.Join(headerNames(headers), ", "), host)
}

// headerNames lists configured header names in sorted order.
func headerNames(headers map[string]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// redirectPolicy refuses a redirect that leaves the origin or downgrades the
// scheme, so the configured headers and the session stay on one origin.
func redirectPolicy(server string) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("mcp: server %q: too many redirects", server)
		}
		previous := via[len(via)-1].URL
		if !strings.EqualFold(req.URL.Host, previous.Host) {
			return fmt.Errorf("mcp: server %q: refused a redirect to another host", server)
		}
		if previous.Scheme == "https" && req.URL.Scheme == "http" {
			return fmt.Errorf("mcp: server %q: refused a redirect from https to http", server)
		}
		return nil
	}
}

// maskError replaces every resolved header value in an error message so a
// transport error can never print a secret.
func maskError(err error, values []string) error {
	if err == nil || len(values) == 0 {
		return err
	}
	msg := err.Error()
	masked := msg
	for _, value := range values {
		if value == "" {
			continue
		}
		masked = strings.ReplaceAll(masked, value, "[REDACTED]")
	}
	if masked == msg {
		return err
	}
	return errors.New(masked)
}

// isLocalHost reports a loopback, private, or link-local host. An
// unresolvable name counts as non-local so a plain-HTTP header is refused.
func isLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "" {
		return false
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ipLocal(ip)
	}
	ips, err := net.LookupIP(h)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ipLocal(ip) {
			return false
		}
	}
	return true
}

func ipLocal(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
