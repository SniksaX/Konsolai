package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	reachTimeout       = 2 * time.Second
	remoteReachTimeout = 3 * time.Second
	reachTTL           = 5 * time.Minute
	reachFailTTL       = 30 * time.Second
	probeBodyLimit     = 1 << 20
)

// ProbeKind classifies one model endpoint probe. A probe only reports what
// the endpoint said; a chat completion failure is never a probe error.
type ProbeKind string

const (
	ProbeOK          ProbeKind = "ok"          // the endpoint answered with a model list
	ProbeSlow        ProbeKind = "slow"        // the probe deadline hit
	ProbeAuthFailed  ProbeKind = "auth failed" // 401 or 403, or the key could not be sent
	ProbeHTTPError   ProbeKind = "http error"  // any other non-2xx status
	ProbeUnreachable ProbeKind = "unreachable" // dial, DNS, TLS, or a refused connection
)

// ProbeResult is one endpoint probe. Status is the HTTP status, zero when no
// response arrived; IDs are the served model ids when the list was read.
type ProbeResult struct {
	Kind     ProbeKind
	Status   int
	Endpoint string
	Latency  time.Duration
	IDs      []string
	Err      error
}

// ProbeError carries a probe outcome as an error. Only model-list reads
// return it; chat completions keep their own HTTPError.
type ProbeError struct {
	Kind     ProbeKind
	Status   int
	Endpoint string
	Err      error
}

func (e *ProbeError) Error() string {
	if e == nil {
		return "llm: probe failed"
	}
	msg := "llm: probe " + string(e.Kind)
	if e.Status > 0 {
		msg += fmt.Sprintf(" (http %d)", e.Status)
	}
	if e.Endpoint != "" {
		msg += " at " + e.Endpoint
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ProbeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AsProbe reports whether err is a probe error and returns it.
func AsProbe(err error) (*ProbeError, bool) {
	var pe *ProbeError
	if !errors.As(err, &pe) {
		return nil, false
	}
	return pe, true
}

func (r ProbeResult) asError() error {
	if r.Kind == ProbeOK {
		return nil
	}
	return &ProbeError{Kind: r.Kind, Status: r.Status, Endpoint: r.Endpoint, Err: r.Err}
}

// SetProbeTimeout sets the budget for one probe of a local endpoint. Zero
// keeps the built-in 2s.
func (c *Client) SetProbeTimeout(d time.Duration) {
	if c == nil {
		return
	}
	c.probeTimeout = d
}

// SetRemoteProbeTimeout sets the budget for one probe of a non-local
// endpoint. Zero keeps the built-in 3s.
func (c *Client) SetRemoteProbeTimeout(d time.Duration) {
	if c == nil {
		return
	}
	c.remoteProbeTimeout = d
}

func (c *Client) probeBudget() time.Duration {
	local := c.probeTimeout
	if local <= 0 {
		local = reachTimeout
	}
	if isRemoteEndpoint(c.baseURL) {
		if c.remoteProbeTimeout > 0 {
			return c.remoteProbeTimeout
		}
		return remoteReachTimeout
	}
	return local
}

// isRemoteEndpoint treats a host that is not loopback, private, or link
// local, or a host that does not resolve, as remote.
func isRemoteEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	h := u.Hostname()
	if h == "" {
		return false
	}
	return !localPlainHost(h)
}

type reachEntry struct {
	at  time.Time
	res ProbeResult
}

var reachMu sync.Mutex
var reachCache = map[string]reachEntry{}

// ResetReachCache clears the process-wide reachability cache (tests).
func ResetReachCache() {
	reachMu.Lock()
	reachCache = map[string]reachEntry{}
	reachMu.Unlock()
}

// Reachable reports whether /models answers with a 2xx. The result is cached
// per endpoint: a good result for 5 minutes and a failed one for 30 seconds,
// so a key fixed after a failure is picked up quickly.
func (c *Client) Reachable(ctx context.Context) bool {
	if c == nil {
		return false
	}
	key := c.baseURL
	reachMu.Lock()
	if e, ok := reachCache[key]; ok {
		ttl := reachTTL
		if e.res.Kind != ProbeOK {
			ttl = reachFailTTL
		}
		if time.Since(e.at) < ttl {
			reachMu.Unlock()
			return e.res.Kind == ProbeOK
		}
	}
	reachMu.Unlock()
	res := c.Probe(ctx)
	reachMu.Lock()
	reachCache[key] = reachEntry{at: time.Now(), res: res}
	reachMu.Unlock()
	return res.Kind == ProbeOK
}

// Probe reads /models once and classifies the outcome. It is uncached, so a
// status or doctor line always reflects the endpoint now.
func (c *Client) Probe(ctx context.Context) ProbeResult {
	res := ProbeResult{Endpoint: c.baseURL}
	if c == nil {
		res.Kind = ProbeUnreachable
		return res
	}
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, c.probeBudget())
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		res.Kind = ProbeUnreachable
		res.Err = fmt.Errorf("llm: build models request: %w", err)
		res.Latency = time.Since(start)
		return res
	}
	key, err := c.authorize(req)
	if err != nil {
		res.Kind = ProbeAuthFailed
		res.Err = err
		res.Latency = time.Since(start)
		return res
	}
	resp, err := c.http.Do(req)
	res.Latency = time.Since(start)
	if err != nil {
		res.Kind = probeTransportKind(pctx, err)
		res.Err = fmt.Errorf("llm: list models: %w", err)
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		res.Kind = ProbeAuthFailed
		res.Err = httpErrorKey(resp, key)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		res.Kind = ProbeHTTPError
		res.Err = httpErrorKey(resp, key)
	default:
		res.Kind = ProbeOK
		res.IDs = readModelIDs(resp.Body)
	}
	return res
}

func probeTransportKind(ctx context.Context, err error) ProbeKind {
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ProbeSlow
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ProbeSlow
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ProbeSlow
	}
	return ProbeUnreachable
}

// readModelIDs reads the served ids from a models payload and ignores a body
// it cannot parse; the endpoint still answered.
func readModelIDs(r io.Reader) []string {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(r, probeBodyLimit)).Decode(&body); err != nil {
		return nil
	}
	out := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out
}

// ServedIDs lists the endpoint's model ids or returns the probe error.
func (c *Client) ServedIDs(ctx context.Context) ([]string, error) {
	res := c.Probe(ctx)
	if err := res.asError(); err != nil {
		return nil, err
	}
	return res.IDs, nil
}
