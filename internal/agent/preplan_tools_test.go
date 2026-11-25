package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// TestBeforePlanOnlyReadOnlyTools proves the forced-plan waste is gone: the
// first build request offers read-only tools plus propose_plan, never a
// mutating tool whose call would only be rejected.
func TestBeforePlanOnlyReadOnlyTools(t *testing.T) {
	var requests []map[string]bool
	reply := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" || r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":16384}]}`))
			return
		}
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		names := map[string]bool{}
		for _, tl := range body.Tools {
			names[tl.Function.Name] = true
		}
		requests = append(requests, names)
		w.Header().Set("Content-Type", "application/json")
		if reply == 0 {
			reply++
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"propose_plan","arguments":"{\"summary\":\"a\",\"steps\":[\"a\"],\"files\":[\"a.txt\"],\"commands\":[]}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":5}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":5}}`)
	}))
	defer srv.Close()
	client := llm.New(srv.URL+"/v1", "m")
	client.PinContext(16384)
	client.SetRetry(0, 0)
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, Err: io.Discard,
		RequirePlan: true, MaxSteps: 6, Intent: "build",
	}, "sys", "change a file")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(requests) < 2 {
		t.Fatalf("requests %v", requests)
	}
	first := requests[0]
	if !first["propose_plan"] || !first["read_file"] {
		t.Fatalf("pre-plan request misses the read-only set: %v", first)
	}
	for _, bad := range []string{"run_shell", "write_file", "edit_file"} {
		if first[bad] {
			t.Fatalf("pre-plan request offers %s: %v", bad, first)
		}
	}
	later := requests[len(requests)-1]
	if !later["run_shell"] && !later["write_file"] {
		t.Fatalf("after the plan the mutating tools must be offered: %v", later)
	}
}
