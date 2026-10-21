package turn

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/SniksaX/Konsolai/internal/llm"
)

func ModelErrorLine(endpoint string, err error, trace bool) string {
	if err == nil {
		return "konsolai: unknown error"
	}
	if trace {
		return "konsolai: " + err.Error()
	}
	host := EndpointHost(endpoint)
	if errors.Is(err, llm.ErrIdleTimeout) {
		return "konsolai: the model server stopped responding (" + host + "). Try again, or check the server."
	}
	if errors.Is(err, llm.ErrTruncated) {
		return "konsolai: the model's answer was cut off; try a narrower request."
	}
	var he *llm.HTTPError
	if errors.As(err, &he) {
		if he.Status >= 500 {
			return "konsolai: the model server returned an error (" + host + "). Try again."
		}
		if he.Status >= 400 {
			if msg := friendlyErrBody(he.Body); msg != "" {
				return "konsolai: the model server rejected the request: " + msg
			}
			return "konsolai: the model server rejected the request."
		}
	}
	if isDialErr(err) {
		return "konsolai: cannot reach the model server at " + host + ". Run /doctor to check every endpoint."
	}
	s := err.Error()
	if strings.Contains(s, "{") || strings.Contains(s, `"error"`) {
		return "konsolai: the model server returned an error (" + host + "). Try again."
	}
	return "konsolai: " + s
}

func friendlyErrBody(s string) string {
	s = firstErrLine(s)
	if s == "" || strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return ""
	}
	return s
}

func EndpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err == nil && u.Host != "" {
		return u.Host
	}
	s := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "the model server"
	}
	return s
}

func firstErrLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isDialErr(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "no such host") || strings.Contains(s, "dial tcp")
}
