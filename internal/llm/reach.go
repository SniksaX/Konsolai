package llm

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const (
	reachTimeout = 2 * time.Second
	reachTTL     = 5 * time.Minute
)

type reachEntry struct {
	at time.Time
	ok bool
}

var reachMu sync.Mutex
var reachCache = map[string]reachEntry{}

// ResetReachCache clears the process-wide reachability cache (tests).
func ResetReachCache() {
	reachMu.Lock()
	reachCache = map[string]reachEntry{}
	reachMu.Unlock()
}

// Reachable reports whether /models answers within 2s. The result is cached
// per endpoint for 5 minutes so the REPL re-probes on that interval.
func (c *Client) Reachable(ctx context.Context) bool {
	key := c.baseURL
	reachMu.Lock()
	if e, ok := reachCache[key]; ok && time.Since(e.at) < reachTTL {
		reachMu.Unlock()
		return e.ok
	}
	reachMu.Unlock()
	ok := c.probeModels(ctx)
	reachMu.Lock()
	reachCache[key] = reachEntry{at: time.Now(), ok: ok}
	reachMu.Unlock()
	return ok
}

func (c *Client) probeModels(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, reachTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
