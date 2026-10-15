package llm

import (
	"bufio"
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
	"syscall"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

var (
	ErrTruncated   = errors.New("model output was cut off (max tokens)")
	ErrIdleTimeout = errors.New("llm: idle timeout")
)

const (
	defaultIdleTimeout   = 120 * time.Second
	defaultRetryWait     = 2 * time.Second
	progressPeriod       = 200 * time.Millisecond
	defaultContextWindow = 32768
	defaultTokensPerChar = 0.30
	tokensPerCharAlpha   = 0.5
)

type Progress struct {
	ReasoningTokens int
	ContentTokens   int
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("llm: %d: %s", e.Status, strings.TrimSpace(e.Body))
}

type Client struct {
	baseURL       string
	model         string
	http          *http.Client
	idleTimeout   time.Duration
	retryWait     time.Duration
	mu            sync.Mutex
	contextWindow int
	windowReady   bool
	tokensPerChar float64
}

func New(baseURL, model string) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	return &Client{
		baseURL:       strings.TrimRight(baseURL, "/"),
		model:         model,
		http:          &http.Client{Transport: tr},
		idleTimeout:   defaultIdleTimeout,
		retryWait:     defaultRetryWait,
		contextWindow: defaultContextWindow,
		tokensPerChar: defaultTokensPerChar,
	}
}

func (c *Client) ContextWindow() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contextWindow <= 0 {
		return defaultContextWindow
	}
	return c.contextWindow
}

func (c *Client) TokensPerChar() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokensPerChar <= 0 {
		return defaultTokensPerChar
	}
	return c.tokensPerChar
}

func MessageChars(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		n += len(m.ToolCallID)
		for _, tc := range m.ToolCalls {
			n += len(tc.ID) + len(tc.Type) + len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n
}

func ToolsSchemaChars(tools []ToolSpec) int {
	n := 0
	for _, t := range tools {
		n += len(t.Type) + len(t.Function.Name) + len(t.Function.Description) + len(t.Function.Parameters)
	}
	return n
}

func (c *Client) EstimatePromptTokens(msgs []Message, tools []ToolSpec) int {
	ratio := c.TokensPerChar()
	return int(float64(MessageChars(msgs))*ratio + float64(ToolsSchemaChars(tools))*ratio)
}

func (c *Client) observePrompt(msgs []Message, usage Usage) {
	chars := MessageChars(msgs)
	if usage.PromptTokens <= 0 || chars <= 0 {
		return
	}
	observed := float64(usage.PromptTokens) / float64(chars)
	c.mu.Lock()
	c.tokensPerChar = tokensPerCharAlpha*observed + (1-tokensPerCharAlpha)*c.tokensPerChar
	c.mu.Unlock()
}

func (c *Client) setWindow(n int) {
	if n <= 0 {
		n = defaultContextWindow
	}
	c.mu.Lock()
	c.contextWindow = n
	c.windowReady = true
	c.mu.Unlock()
}

func (c *Client) SetIdleTimeout(d time.Duration) {
	if d > 0 {
		c.idleTimeout = d
	}
}

// ResolveModel returns the configured model or the first model advertised by /models
// and reads max_model_len for the resolved model (fallback 32768).
func (c *Client) ResolveModel(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.model != "" && c.windowReady {
		id := c.model
		c.mu.Unlock()
		return id, nil
	}
	want := c.model
	c.mu.Unlock()

	fetchCtx := ctx
	if want != "" {
		var cancel context.CancelFunc
		fetchCtx, cancel = context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		if want != "" {
			c.setWindow(defaultContextWindow)
			return want, nil
		}
		return "", fmt.Errorf("llm: build models request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if want != "" {
			c.setWindow(defaultContextWindow)
			return want, nil
		}
		return "", fmt.Errorf("llm: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if want != "" {
			c.setWindow(defaultContextWindow)
			return want, nil
		}
		return "", httpError(resp)
	}
	var body struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		if want != "" {
			c.setWindow(defaultContextWindow)
			return want, nil
		}
		return "", fmt.Errorf("llm: decode models: %w", err)
	}
	if len(body.Data) == 0 || (want == "" && body.Data[0].ID == "") {
		if want != "" {
			c.setWindow(defaultContextWindow)
			return want, nil
		}
		return "", errors.New("llm: server advertises no models, set KONSOLAI_MODEL")
	}

	chosen := body.Data[0]
	if want != "" {
		found := false
		for _, m := range body.Data {
			if m.ID == want {
				chosen = m
				found = true
				break
			}
		}
		if !found {
			chosen.ID = want
			chosen.MaxModelLen = 0
		}
	}
	c.mu.Lock()
	if c.model == "" {
		c.model = chosen.ID
	}
	win := chosen.MaxModelLen
	if win <= 0 {
		win = defaultContextWindow
	}
	c.contextWindow = win
	c.windowReady = true
	id := c.model
	c.mu.Unlock()
	return id, nil
}

type ChatOptions struct {
	Thinking       bool
	Temperature    float64
	MaxTokens      int
	ResponseSchema json.RawMessage
	OnProgress     func(Progress)
	IdleTimeout    time.Duration
	// ToolChoice is "" (auto), "required", or a function name sent as a named tool_choice.
	ToolChoice string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type usageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type responseFormat struct {
	Type       string         `json:"type"`
	JSONSchema jsonSchemaSpec `json:"json_schema"`
}

type jsonSchemaSpec struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type chatRequest struct {
	Model              string             `json:"model"`
	Messages           []Message          `json:"messages"`
	Stream             bool               `json:"stream"`
	Temperature        float64            `json:"temperature"`
	MaxTokens          int                `json:"max_tokens,omitempty"`
	ChatTemplateKwargs chatTemplateKwargs `json:"chat_template_kwargs"`
	Tools              []ToolSpec         `json:"tools,omitempty"`
	ToolChoice         json.RawMessage    `json:"tool_choice,omitempty"`
	ResponseFormat     *responseFormat    `json:"response_format,omitempty"`
	StreamOptions      *streamOptions     `json:"stream_options,omitempty"`
}

type completeResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *usageJSON `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type chatTemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking"`
}

type streamDeltaTool struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string            `json:"content"`
			Reasoning        string            `json:"reasoning"`
			ReasoningContent string            `json:"reasoning_content"`
			ToolCalls        []streamDeltaTool `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageJSON `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func usageOf(u *usageJSON) Usage {
	if u == nil {
		return Usage{}
	}
	return Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}
}

// Complete sends a streaming chat completion and returns the assembled assistant message.
func (c *Client) Complete(ctx context.Context, msgs []Message, tools []ToolSpec, opts ChatOptions) (Message, Usage, error) {
	return c.complete(ctx, msgs, tools, opts, nil)
}

// Stream sends a chat completion request and invokes onDelta for each content token.
func (c *Client) Stream(ctx context.Context, msgs []Message, opts ChatOptions, onDelta func(string) error) (Usage, error) {
	_, usage, err := c.complete(ctx, msgs, nil, opts, onDelta)
	return usage, err
}

func (c *Client) complete(ctx context.Context, msgs []Message, tools []ToolSpec, opts ChatOptions, onDelta func(string) error) (Message, Usage, error) {
	msg, usage, err, gotBytes := c.completeAttempt(ctx, msgs, tools, opts, onDelta)
	if err == nil || !shouldRetry(err, gotBytes) {
		c.observePrompt(msgs, usage)
		return msg, usage, err
	}
	wait := c.retryWait
	if wait <= 0 {
		wait = defaultRetryWait
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return Message{}, Usage{}, ctx.Err()
	case <-t.C:
	}
	msg, usage, err, _ = c.completeAttempt(ctx, msgs, tools, opts, onDelta)
	c.observePrompt(msgs, usage)
	return msg, usage, err
}

func encodeToolChoice(s string) json.RawMessage {
	switch s {
	case "", "auto":
		return json.RawMessage(`"auto"`)
	case "required":
		return json.RawMessage(`"required"`)
	default:
		b, err := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": s},
		})
		if err != nil {
			return json.RawMessage(`"auto"`)
		}
		return b
	}
}

func (c *Client) completeAttempt(ctx context.Context, msgs []Message, tools []ToolSpec, opts ChatOptions, onDelta func(string) error) (Message, Usage, error, bool) {
	model, err := c.ResolveModel(ctx)
	if err != nil {
		return Message{}, Usage{}, err, false
	}
	reqBody := chatRequest{
		Model:              model,
		Messages:           msgs,
		Stream:             true,
		Temperature:        opts.Temperature,
		MaxTokens:          opts.MaxTokens,
		ChatTemplateKwargs: chatTemplateKwargs{EnableThinking: opts.Thinking},
		StreamOptions:      &streamOptions{IncludeUsage: true},
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
		reqBody.ToolChoice = encodeToolChoice(opts.ToolChoice)
	}
	if len(opts.ResponseSchema) > 0 {
		reqBody.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaSpec{
				Name:   "out",
				Schema: opts.ResponseSchema,
				Strict: true,
			},
		}
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("llm: encode request: %w", err), false
	}

	idle := opts.IdleTimeout
	if idle <= 0 {
		idle = c.idleTimeout
	}
	if idle <= 0 {
		idle = defaultIdleTimeout
	}
	reqCtx, stopIdle := context.WithCancel(ctx)
	timer := time.AfterFunc(idle, stopIdle)
	defer func() {
		timer.Stop()
		stopIdle()
	}()
	touch := func() { timer.Reset(idle) }

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("llm: build chat request: %w", err), false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, Usage{}, mapRequestError(ctx, reqCtx, err), false
	}
	defer resp.Body.Close()
	gotBytes := true
	if resp.StatusCode != http.StatusOK {
		return Message{}, Usage{}, httpError(resp), true
	}

	body := &touchReader{r: resp.Body, touch: touch}
	br := bufio.NewReader(body)
	preview, peekErr := br.Peek(5)
	if peekErr != nil && peekErr != io.EOF {
		return Message{}, Usage{}, mapRequestError(ctx, reqCtx, fmt.Errorf("llm: read stream: %w", peekErr)), gotBytes
	}
	if !isSSE(preview) {
		msg, usage, err := decodeJSONCompletion(br, opts)
		return msg, usage, err, gotBytes
	}
	msg, usage, err := readSSE(ctx, reqCtx, br, opts, onDelta)
	return msg, usage, err, gotBytes
}

func isSSE(preview []byte) bool {
	p := bytes.TrimSpace(preview)
	return bytes.HasPrefix(p, []byte("data:")) || bytes.HasPrefix(p, []byte("data"))
}

func decodeJSONCompletion(r io.Reader, opts ChatOptions) (Message, Usage, error) {
	var body completeResponse
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return Message{}, Usage{}, fmt.Errorf("llm: decode completion: %w", err)
	}
	if body.Error != nil {
		return Message{}, Usage{}, fmt.Errorf("llm: server error: %s", body.Error.Message)
	}
	if len(body.Choices) == 0 {
		return Message{}, Usage{}, errors.New("llm: no choices")
	}
	msg := body.Choices[0].Message
	msg.Role = RoleAssistant
	if opts.OnProgress != nil {
		opts.OnProgress(Progress{ContentTokens: len(msg.Content) / 4})
	}
	return msg, usageOf(body.Usage), nil
}

func readSSE(parent, reqCtx context.Context, r io.Reader, opts ChatOptions, onDelta func(string) error) (Message, Usage, error) {
	acc := newAccum()
	prog := newProgress(opts.OnProgress)
	defer prog.flush()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if bytes.Equal(data, []byte("[DONE]")) {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return Message{}, Usage{}, fmt.Errorf("llm: decode chunk: %w", err)
		}
		if chunk.Error != nil {
			return Message{}, Usage{}, fmt.Errorf("llm: server error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			acc.usage = usageOf(chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			acc.saw = true
			if choice.FinishReason != "" {
				acc.finish = choice.FinishReason
			}
			d := choice.Delta
			if d.Content != "" {
				acc.content.WriteString(d.Content)
				prog.add(0, len(d.Content))
				if onDelta != nil {
					if err := onDelta(d.Content); err != nil {
						return Message{}, Usage{}, err
					}
				}
			}
			reason := d.Reasoning + d.ReasoningContent
			if reason != "" {
				prog.add(len(reason), 0)
			}
			for _, tc := range d.ToolCalls {
				acc.addTool(tc)
				if tc.Function.Arguments != "" {
					prog.add(0, len(tc.Function.Arguments))
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Message{}, Usage{}, mapRequestError(parent, reqCtx, fmt.Errorf("llm: read stream: %w", err))
	}
	msg, err := acc.message()
	if err != nil {
		return msg, acc.usage, err
	}
	if acc.finish == "length" {
		return msg, acc.usage, ErrTruncated
	}
	if !acc.saw && msg.Content == "" && len(msg.ToolCalls) == 0 {
		return Message{}, acc.usage, errors.New("llm: no choices")
	}
	return msg, acc.usage, nil
}

type accum struct {
	content strings.Builder
	calls   map[int]*ToolCall
	finish  string
	usage   Usage
	saw     bool
}

func newAccum() *accum {
	return &accum{calls: make(map[int]*ToolCall)}
}

func (a *accum) addTool(d streamDeltaTool) {
	tc, ok := a.calls[d.Index]
	if !ok {
		tc = &ToolCall{Type: "function"}
		a.calls[d.Index] = tc
	}
	if d.ID != "" {
		tc.ID = d.ID
	}
	if d.Type != "" {
		tc.Type = d.Type
	}
	if d.Function.Name != "" {
		tc.Function.Name = d.Function.Name
	}
	if d.Function.Arguments != "" {
		tc.Function.Arguments += d.Function.Arguments
	}
}

func (a *accum) message() (Message, error) {
	msg := Message{Role: RoleAssistant, Content: a.content.String()}
	if len(a.calls) == 0 {
		return msg, nil
	}
	max := 0
	for i := range a.calls {
		if i > max {
			max = i
		}
	}
	for i := 0; i <= max; i++ {
		tc, ok := a.calls[i]
		if !ok {
			continue
		}
		_ = json.Valid([]byte(tc.Function.Arguments))
		msg.ToolCalls = append(msg.ToolCalls, *tc)
	}
	return msg, nil
}

type progressPub struct {
	fn           func(Progress)
	reasonChars  int
	contentChars int
	last         time.Time
	pending      bool
}

func newProgress(fn func(Progress)) *progressPub {
	return &progressPub{fn: fn}
}

func (p *progressPub) add(reason, content int) {
	p.reasonChars += reason
	p.contentChars += content
	if p.fn == nil {
		return
	}
	now := time.Now()
	if !p.last.IsZero() && now.Sub(p.last) < progressPeriod {
		p.pending = true
		return
	}
	p.emit()
}

func (p *progressPub) emit() {
	p.fn(Progress{ReasoningTokens: p.reasonChars / 4, ContentTokens: p.contentChars / 4})
	p.last = time.Now()
	p.pending = false
}

func (p *progressPub) flush() {
	if p.pending && p.fn != nil {
		p.emit()
	}
}

type touchReader struct {
	r     io.Reader
	touch func()
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.touch()
	}
	return n, err
}

func mapRequestError(parent, reqCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if reqCtx.Err() != nil {
		return ErrIdleTimeout
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrIdleTimeout
	}
	if _, ok := err.(*HTTPError); ok {
		return err
	}
	if strings.HasPrefix(err.Error(), "llm:") {
		return err
	}
	return fmt.Errorf("llm: chat: %w", err)
}

func shouldRetry(err error, gotBytes bool) bool {
	if err == nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusBadGateway || he.Status == http.StatusServiceUnavailable || he.Status == http.StatusGatewayTimeout
	}
	if gotBytes {
		return false
	}
	if errors.Is(err, ErrIdleTimeout) {
		return true
	}
	return isRefusedOrReset(err)
}

func isRefusedOrReset(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		if errors.Is(op.Err, syscall.ECONNREFUSED) || errors.Is(op.Err, syscall.ECONNRESET) {
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "connection reset")
}

func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &HTTPError{Status: resp.StatusCode, Body: string(b)}
}
