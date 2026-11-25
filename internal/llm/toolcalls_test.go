package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testCall(id string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: FunctionCall{Name: "read_file"}}
}

func TestRepairToolCallsCompletesParallelGroupsInOrder(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{testCall("c2"), testCall("c1")}},
		{Role: RoleTool, ToolCallID: "c1", Content: "one"},
	}
	out := RepairToolCalls(msgs)
	if len(out) != 5 {
		t.Fatalf("repaired list %s", describeRoles(out))
	}
	if out[3].ToolCallID != "c2" || out[3].Content != MissingToolReply {
		t.Fatalf("first reply %+v", out[3])
	}
	if out[4].ToolCallID != "c1" || out[4].Content != "one" {
		t.Fatalf("second reply %+v", out[4])
	}
	if err := deepSeekPairsError(out); err != nil {
		t.Fatalf("still invalid: %v", err)
	}
}

func TestRepairToolCallsDropsOrphansAndExtras(t *testing.T) {
	msgs := []Message{
		{Role: RoleTool, ToolCallID: "stray", Content: "orphan"},
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{testCall("c1")}},
		{Role: RoleTool, ToolCallID: "c1", Content: "one"},
		{Role: RoleTool, ToolCallID: "c1", Content: "duplicate"},
		{Role: RoleTool, ToolCallID: "other", Content: "extra"},
		{Role: RoleUser, Content: "next"},
	}
	out := RepairToolCalls(msgs)
	if len(out) != 4 {
		t.Fatalf("repaired list %s", describeRoles(out))
	}
	if out[2].Content != "one" {
		t.Fatalf("kept reply %+v", out[2])
	}
	if err := deepSeekPairsError(out); err != nil {
		t.Fatalf("still invalid: %v", err)
	}
}

func TestRepairToolCallsKeepsReasoningOnTheAssistant(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, ReasoningContent: "thinking", ToolCalls: []ToolCall{testCall("c1")}},
	}
	out := RepairToolCalls(msgs)
	if out[1].ReasoningContent != "thinking" {
		t.Fatalf("reasoning lost: %+v", out[1])
	}
	if len(out) != 3 || out[2].ToolCallID != "c1" {
		t.Fatalf("repaired list %s", describeRoles(out))
	}
}

func describeRoles(msgs []Message) string {
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		parts[i] = string(m.Role)
		if m.Role == RoleTool {
			parts[i] += ":" + m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			parts[i] += " calls:" + m.ToolCalls[0].ID
		}
	}
	return strings.Join(parts, " ; ")
}

// deepSeekPairsError is the DeepSeek pairing rule used by the tests.
func deepSeekPairsError(msgs []Message) error {
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == RoleTool {
			return fmt.Errorf("message %d is an orphan tool reply %q", i, m.ToolCallID)
		}
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		got := 0
		for j := i + 1; j < len(msgs) && msgs[j].Role == RoleTool; j++ {
			if got >= len(m.ToolCalls) || msgs[j].ToolCallID != m.ToolCalls[got].ID {
				return fmt.Errorf("assistant %d: reply order", i)
			}
			got++
		}
		if got != len(m.ToolCalls) {
			return fmt.Errorf("assistant %d: %d calls but %d replies", i, len(m.ToolCalls), got)
		}
		i += got
	}
	return nil
}

// TestClientRepairsAnInvalidListBeforeSending proves the guard runs for every
// request: a deliberately broken caller list still reaches the server valid.
func TestClientRepairsAnInvalidListBeforeSending(t *testing.T) {
	var sent []Message
	var violation string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent = body.Messages
		if err := deepSeekPairsError(sent); err != nil {
			violation = err.Error()
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	_, _, err := c.Complete(context.Background(), []Message{
		{Role: RoleUser, Content: "go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{testCall("c2"), testCall("c1")}},
		{Role: RoleTool, ToolCallID: "c1", Content: "one"},
	}, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("client sent an invalid list: %v", err)
	}
	if violation != "" {
		t.Fatalf("server saw %s: %s", violation, describeRoles(sent))
	}
	if len(sent) != 4 || sent[2].ToolCallID != "c2" || sent[2].Content != MissingToolReply {
		t.Fatalf("sent list %s", describeRoles(sent))
	}
}
