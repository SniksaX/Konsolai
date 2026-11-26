package mcp

import (
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool is one server tool as the bridge sees it.
type Tool struct {
	Name        string
	Description string
	// InputSchema is the raw JSON schema the server published.
	InputSchema json.RawMessage
	// ReadOnly is the server's readOnlyHint annotation.
	ReadOnly bool
}

// Content is one raw tool result block.
type Content struct {
	// Type is the MCP content type: text, image, audio, or resource.
	Type string
	// Text is set for text content.
	Text string
	// Data is the decoded payload for image and audio content.
	Data []byte
	// MIMEType is set for image, audio, and resource content.
	MIMEType string
	// URI and Name describe resource content.
	URI  string
	Name string
}

// Result is a raw tools/call result. The bridge decides how to render it.
type Result struct {
	Content []Content
	// Structured is the optional structuredContent payload.
	Structured json.RawMessage
	// IsError reports a tool-level error; the call itself succeeded.
	IsError bool
}

// toolsOf converts a page of SDK tools.
func toolsOf(page *sdk.ListToolsResult) ([]Tool, error) {
	if page == nil {
		return nil, nil
	}
	out := make([]Tool, 0, len(page.Tools))
	for _, t := range page.Tools {
		if t == nil || t.Name == "" {
			continue
		}
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %q: encode input schema: %w", t.Name, err)
		}
		out = append(out, Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			ReadOnly:    t.Annotations != nil && t.Annotations.ReadOnlyHint,
		})
	}
	return out, nil
}

// resultOf converts a raw SDK call result into the package result, refusing a
// result larger than limit bytes so a hostile server cannot exhaust memory.
func resultOf(name, tool string, r *sdk.CallToolResult, limit int) (Result, error) {
	var out Result
	if r == nil {
		return out, fmt.Errorf("mcp: server %q: tool %q returned no result", name, tool)
	}
	out.IsError = r.IsError
	total := 0
	for _, block := range r.Content {
		switch c := block.(type) {
		case *sdk.TextContent:
			out.Content = append(out.Content, Content{Type: "text", Text: c.Text})
			total += len(c.Text)
		case *sdk.ImageContent:
			out.Content = append(out.Content, Content{Type: "image", Data: c.Data, MIMEType: c.MIMEType})
			total += len(c.Data)
		case *sdk.AudioContent:
			out.Content = append(out.Content, Content{Type: "audio", Data: c.Data, MIMEType: c.MIMEType})
			total += len(c.Data)
		case *sdk.ResourceLink:
			out.Content = append(out.Content, Content{Type: "resource", URI: c.URI, Name: c.Name, MIMEType: c.MIMEType})
			total += len(c.URI) + len(c.Name)
		case *sdk.EmbeddedResource:
			item := Content{Type: "resource"}
			if c.Resource != nil {
				item.URI = c.Resource.URI
				item.MIMEType = c.Resource.MIMEType
				item.Text = c.Resource.Text
				item.Data = c.Resource.Blob
				total += len(c.Resource.Text) + len(c.Resource.Blob) + len(c.Resource.URI)
			}
			out.Content = append(out.Content, item)
		default:
			b, err := json.Marshal(block)
			if err != nil {
				continue
			}
			out.Content = append(out.Content, Content{Type: "unknown", Text: string(b)})
			total += len(b)
		}
	}
	if r.StructuredContent != nil {
		b, err := json.Marshal(r.StructuredContent)
		if err != nil {
			return Result{}, fmt.Errorf("mcp: server %q: tool %q: encode structured content: %w", name, tool, err)
		}
		out.Structured = b
		total += len(b)
	}
	if limit > 0 && total > limit {
		return Result{}, fmt.Errorf("%w: server %q: tool %q returned %d bytes, limit %d", ErrResultTooLarge, name, tool, total, limit)
	}
	return out, nil
}
