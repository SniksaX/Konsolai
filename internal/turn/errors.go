package turn

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/redact"
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
	if errors.Is(err, llm.ErrReasoningTruncated) {
		return "konsolai: the model spent its whole output budget on reasoning before writing an answer; raise llm.max_tokens or set reasoning_effort to none."
	}
	var he *llm.HTTPError
	if errors.As(err, &he) {
		return httpErrorLine(host, he)
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

// httpErrorLine reports a rejected chat call with its HTTP status and the
// provider's own message. It runs in every mode, not only with --trace, and the
// message is masked before it reaches the terminal.
func httpErrorLine(host string, he *llm.HTTPError) string {
	status := fmt.Sprintf("HTTP %d", he.Status)
	detail := redact.String(providerMessage(he.Body))
	if he.Status >= 500 {
		if detail != "" {
			return fmt.Sprintf("konsolai: the model server returned an error (%s) at %s: %s. Try again.", status, host, detail)
		}
		return fmt.Sprintf("konsolai: the model server returned an error (%s) at %s; it sent no error message. Try again.", status, host)
	}
	if he.Status >= 400 {
		if detail != "" {
			return fmt.Sprintf("konsolai: the model server rejected the request (%s): %s", status, detail)
		}
		return fmt.Sprintf("konsolai: the model server rejected the request (%s); it sent no error message.", status)
	}
	return fmt.Sprintf("konsolai: the model server answered %s at %s.", status, host)
}

// rawErrorText reads the error value of an OpenAI-style body, which may be an
// object with message/detail/type or a plain string.
func rawErrorText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return firstErrLine(s)
	}
	var obj struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
		Type    string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	for _, candidate := range []string{obj.Message, obj.Detail, obj.Type} {
		if line := firstErrLine(candidate); line != "" {
			return line
		}
	}
	return ""
}

// providerMessage extracts the provider's error text from an OpenAI-style body
// ({"error":{"message":"..."}}) and falls back to a single plain line.
func providerMessage(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var payload struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	if json.Unmarshal([]byte(body), &payload) == nil {
		if line := rawErrorText(payload.Error); line != "" {
			return line
		}
		for _, candidate := range []string{payload.Message, payload.Detail} {
			if line := firstErrLine(candidate); line != "" {
				return line
			}
		}
		if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
			return ""
		}
	}
	return firstErrLine(body)
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
