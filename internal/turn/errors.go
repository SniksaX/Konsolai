package turn

import (
	"errors"
	"fmt"
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
	if probe, ok := llm.AsProbe(err); ok {
		return probeErrorLine(host, probe)
	}
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

// probeErrorLine reports a failed model-list probe. A probe only says what the
// endpoint answered: an HTTP status or a timeout never proves the host is down,
// and a probe failure means the turn made no chat call. Only a failed chat call
// may use the cannot-reach wording.
func probeErrorLine(host string, probe *llm.ProbeError) string {
	switch probe.Kind {
	case llm.ProbeAuthFailed:
		if probe.Status > 0 {
			return fmt.Sprintf("konsolai: the model endpoint at %s answered HTTP %d; the host is reachable, so check the API key with /set key.", host, probe.Status)
		}
		return "konsolai: the model key for " + host + " could not be sent; check it with /set key."
	case llm.ProbeHTTPError:
		if probe.Status > 0 {
			return fmt.Sprintf("konsolai: the model endpoint at %s answered HTTP %d; the host is reachable, but it did not list its models. Run /doctor.", host, probe.Status)
		}
		return "konsolai: the model endpoint at " + host + " did not list its models. Run /doctor."
	case llm.ProbeSlow:
		return "konsolai: the model endpoint at " + host + " did not answer a model probe in time; this turn made no chat call. Try again, or run /doctor."
	default:
		return "konsolai: a model probe could not reach " + host + "; this turn made no chat call. Run /doctor to check the endpoint."
	}
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
