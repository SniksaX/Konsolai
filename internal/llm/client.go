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

type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

func New(baseURL, model string) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	tr.ResponseHeaderTimeout = 60 * time.Second
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{Transport: tr},
	}
}

// ResolveModel returns the configured model or the first model advertised by /models.
func (c *Client) ResolveModel(ctx context.Context) (string, error) {
	if c.model != "" {
		return c.model, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return "", fmt.Errorf("llm: build models request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", httpError(resp)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("llm: decode models: %w", err)
	}
	if len(body.Data) == 0 || body.Data[0].ID == "" {
		return "", errors.New("llm: server advertises no models, set KONSOLAI_MODEL")
	}
	c.model = body.Data[0].ID
	return c.model, nil
}

type ChatOptions struct {
	Thinking       bool
	Temperature    float64
	MaxTokens      int
	ResponseSchema json.RawMessage
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
	ToolChoice         string             `json:"tool_choice,omitempty"`
	ResponseFormat     *responseFormat    `json:"response_format,omitempty"`
}

type completeResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type chatTemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends a non-streaming chat completion request and returns the assistant message.
func (c *Client) Complete(ctx context.Context, msgs []Message, tools []ToolSpec, opts ChatOptions) (Message, error) {
	model, err := c.ResolveModel(ctx)
	if err != nil {
		return Message{}, err
	}
	reqBody := chatRequest{
		Model:              model,
		Messages:           msgs,
		Stream:             false,
		Temperature:        opts.Temperature,
		MaxTokens:          opts.MaxTokens,
		ChatTemplateKwargs: chatTemplateKwargs{EnableThinking: opts.Thinking},
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
		reqBody.ToolChoice = "auto"
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
		return Message{}, fmt.Errorf("llm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Message{}, fmt.Errorf("llm: build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("llm: chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Message{}, httpError(resp)
	}
	var body completeResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Message{}, fmt.Errorf("llm: decode completion: %w", err)
	}
	if body.Error != nil {
		return Message{}, fmt.Errorf("llm: server error: %s", body.Error.Message)
	}
	if len(body.Choices) == 0 {
		return Message{}, errors.New("llm: no choices")
	}
	msg := body.Choices[0].Message
	msg.Role = RoleAssistant
	return msg, nil
}

// Stream sends a chat completion request and invokes onDelta for each content token.
func (c *Client) Stream(ctx context.Context, msgs []Message, opts ChatOptions, onDelta func(string) error) error {
	model, err := c.ResolveModel(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(chatRequest{
		Model:              model,
		Messages:           msgs,
		Stream:             true,
		Temperature:        opts.Temperature,
		ChatTemplateKwargs: chatTemplateKwargs{EnableThinking: opts.Thinking},
	})
	if err != nil {
		return fmt.Errorf("llm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("llm: build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("llm: chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpError(resp)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if bytes.Equal(data, []byte("[DONE]")) {
			return nil
		}
		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("llm: decode chunk: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("llm: server error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			if err := onDelta(choice.Delta.Content); err != nil {
				return err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("llm: read stream: %w", err)
	}
	return nil
}

func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("llm: %s: %s", resp.Status, strings.TrimSpace(string(b)))
}
