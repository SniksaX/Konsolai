package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/llm"
)

const (
	webFetchTTL     = 24 * time.Hour
	webFetchTimeout = 15 * time.Second
	webFetchMax     = 1 << 20
)

type webFetchTool struct {
	cache  WebCache
	client *http.Client
}

func newWebFetch(cache WebCache) *webFetchTool {
	return &webFetchTool{
		cache:  cache,
		client: &http.Client{Timeout: webFetchTimeout},
	}
}

func (t *webFetchTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "web_fetch",
			Description: "Fetch a public HTTP(S) URL and return extracted text. Results are cached for 24 hours.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"Absolute http or https URL."}},"required":["url"]}`),
		},
	}
}

type webFetchArgs struct {
	URL string `json:"url"`
}

type webFetchAction struct {
	cache  WebCache
	client *http.Client
	url    string
}

func (t *webFetchTool) Prepare(rawArgs string) (Action, error) {
	var args webFetchArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode web_fetch: %w", err)
	}
	u, err := url.Parse(strings.TrimSpace(args.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("tools: web_fetch: url must be an absolute http or https URL")
	}
	return &webFetchAction{cache: t.cache, client: t.client, url: u.String()}, nil
}

func (a *webFetchAction) Summary() string  { return "fetch " + a.url }
func (a *webFetchAction) Preview() string  { return "" }
func (a *webFetchAction) Kind() ActionKind { return ReadOnly }

func (a *webFetchAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if title, text, ok := a.cache.Get(ctx, a.url); ok {
		out := formatFetched(title, text)
		return Result{Output: out, Meta: formatSize(int64(len(out))) + " cached"}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	req.Header.Set("User-Agent", "konsolai")
	resp, err := a.client.Do(req)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{Output: fmt.Sprintf("error: HTTP %d", resp.StatusCode), IsError: true}, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMax+1))
	if err != nil {
		return Result{Output: err.Error(), IsError: true}, nil
	}
	if len(body) > webFetchMax {
		body = body[:webFetchMax]
	}
	if !utf8.Valid(body) {
		return Result{Output: "error: binary content (not UTF-8)", IsError: true}, nil
	}
	title, text := extractPage(string(body), resp.Header.Get("Content-Type"))
	_ = a.cache.Put(ctx, a.url, title, text, webFetchTTL)
	out := formatFetched(title, text)
	return Result{Output: out, Meta: formatSize(int64(len(out)))}, nil
}

func formatFetched(title, text string) string {
	if title == "" {
		return text
	}
	if text == "" {
		return title
	}
	return title + "\n\n" + text
}

func extractPage(raw, contentType string) (title, text string) {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "html") || looksLikeHTML(raw) {
		return htmlTitleText(raw)
	}
	return "", strings.TrimSpace(raw)
}

func looksLikeHTML(s string) bool {
	t := strings.TrimSpace(strings.ToLower(s))
	return strings.HasPrefix(t, "<!doctype html") || strings.HasPrefix(t, "<html")
}

func htmlTitleText(s string) (string, string) {
	lower := strings.ToLower(s)
	title := ""
	if i := strings.Index(lower, "<title>"); i >= 0 {
		rest := s[i+7:]
		if j := strings.Index(strings.ToLower(rest), "</title>"); j >= 0 {
			title = collapseSpace(stripTags(rest[:j]))
		}
	}
	body := s
	if i := strings.Index(lower, "<body"); i >= 0 {
		if j := strings.Index(lower[i:], ">"); j >= 0 {
			body = s[i+j+1:]
		}
	}
	body = stripBlocks(body, "script")
	body = stripBlocks(body, "style")
	return title, collapseSpace(stripTags(body))
}

func stripBlocks(s, tag string) string {
	for {
		low := strings.ToLower(s)
		open := "<" + tag
		i := strings.Index(low, open)
		if i < 0 {
			return s
		}
		closeTag := "</" + tag + ">"
		j := strings.Index(low[i:], closeTag)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+len(closeTag):]
	}
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
