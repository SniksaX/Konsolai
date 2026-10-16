package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	embedBatch    = 32
	embedTimeout  = 15 * time.Second
	rerankTimeout = 10 * time.Second
	maxErrBody    = 512
	retryWait     = 200 * time.Millisecond
)

type Client struct {
	embedURL  string
	rerankURL string
	http      *http.Client
	mu        sync.Mutex
	dim       int
}

type Scored struct {
	Index int
	Score float64
	Text  string
}

func newHTTP(d time.Duration) *http.Client {
	return &http.Client{
		Timeout: d,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ForceAttemptHTTP2:     false,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: d,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func New(embedEndpoint, rerankEndpoint string) *Client {
	return &Client{
		embedURL:  strings.TrimRight(embedEndpoint, "/"),
		rerankURL: strings.TrimRight(rerankEndpoint, "/"),
		http:      newHTTP(embedTimeout),
	}
}

func (c *Client) Dim() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dim
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c == nil || c.embedURL == "" {
		return nil, errors.New("embed: no endpoint")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += embedBatch {
		end := i + embedBatch
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := c.embedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (c *Client) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryWait):
			}
		}
		vecs, err := c.embedOnce(ctx, texts)
		if err == nil {
			return vecs, nil
		}
		last = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, last
}

func (c *Client) embedOnce(ctx context.Context, texts []string) ([][]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"inputs":    texts,
		"normalize": true,
		"truncate":  true,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embedURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("embed: read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &httpStatus{Status: resp.StatusCode, Body: clipErr(raw)}
	}
	vecs, err := parseEmbeddings(raw)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("embed: got %d vectors for %d inputs", len(vecs), len(texts))
	}
	if err := c.checkDim(vecs); err != nil {
		return nil, err
	}
	return vecs, nil
}

func (c *Client) checkDim(vecs [][]float32) error {
	if len(vecs) == 0 {
		return nil
	}
	d := len(vecs[0])
	if d == 0 {
		return errors.New("embed: empty vector")
	}
	for i, v := range vecs {
		if len(v) != d {
			return fmt.Errorf("embed: vector %d has dim %d, want %d", i, len(v), d)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dim == 0 {
		c.dim = d
		return nil
	}
	if d != c.dim {
		return fmt.Errorf("embed: dimension %d does not match cached %d", d, c.dim)
	}
	return nil
}

func parseEmbeddings(raw []byte) ([][]float32, error) {
	var list [][]float32
	if err := json.Unmarshal(raw, &list); err == nil && list != nil {
		return list, nil
	}
	var wrap struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &wrap); err == nil && wrap.Embeddings != nil {
		return wrap.Embeddings, nil
	}
	return nil, fmt.Errorf("embed: unexpected response")
}

func (c *Client) Rerank(ctx context.Context, query string, texts []string) ([]Scored, error) {
	if c == nil || c.rerankURL == "" {
		return nil, errors.New("embed: no rerank endpoint")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, rerankTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"query": query,
		"texts": texts,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: rerank marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rerankURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := newHTTP(rerankTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: rerank: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("embed: rerank read: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, &httpStatus{Status: resp.StatusCode, Body: clipErr(raw)}
	}
	rows, err := parseRerank(raw)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Index >= 0 && rows[i].Index < len(texts) {
			rows[i].Text = texts[rows[i].Index]
		}
	}
	return rows, nil
}

type rerankRow struct {
	Index          int     `json:"index"`
	Score          float64 `json:"score"`
	RelevanceScore float64 `json:"relevance_score"`
}

func parseRerank(raw []byte) ([]Scored, error) {
	var rows []rerankRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		var wrap struct {
			Results []rerankRow `json:"results"`
		}
		if err2 := json.Unmarshal(raw, &wrap); err2 != nil || wrap.Results == nil {
			return nil, fmt.Errorf("embed: rerank parse: %w", err)
		}
		rows = wrap.Results
	}
	out := make([]Scored, len(rows))
	for i, r := range rows {
		score := r.Score
		if r.RelevanceScore != 0 && score == 0 {
			score = r.RelevanceScore
		}
		out[i] = Scored{Index: r.Index, Score: score}
	}
	return out, nil
}

type httpStatus struct {
	Status int
	Body   string
}

func (e *httpStatus) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("embed: HTTP %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("embed: HTTP %d", e.Status)
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	var hs *httpStatus
	if errors.As(err, &hs) && hs.Status >= 500 {
		return true
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "connection reset") || strings.Contains(s, "i/o timeout")
}

func clipErr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxErrBody {
		s = s[:maxErrBody]
	}
	return s
}
