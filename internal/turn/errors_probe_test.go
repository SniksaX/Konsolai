package turn

import (
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

// probeLine is one model-list probe outcome and the wording it must produce.
func TestProbeErrorWording(t *testing.T) {
	const host = "api.example.test"
	cases := []struct {
		name    string
		probe   *llm.ProbeError
		wants   []string
		unwants []string
	}{
		{
			name:  "auth failed",
			probe: &llm.ProbeError{Kind: llm.ProbeAuthFailed, Status: 401, Endpoint: "https://" + host},
			wants: []string{"HTTP 401", "host is reachable", "API key"},
			unwants: []string{
				"cannot reach", "unreachable", "is down",
			},
		},
		{
			name:  "other http status",
			probe: &llm.ProbeError{Kind: llm.ProbeHTTPError, Status: 500},
			wants: []string{"HTTP 500", "host is reachable"},
			unwants: []string{
				"cannot reach", "unreachable", "is down",
			},
		},
		{
			name:  "slow",
			probe: &llm.ProbeError{Kind: llm.ProbeSlow},
			wants: []string{"did not answer a model probe in time", "made no chat call"},
			unwants: []string{
				"cannot reach", "unreachable", "is down", "HTTP",
			},
		},
		{
			name:  "probe could not reach",
			probe: &llm.ProbeError{Kind: llm.ProbeUnreachable},
			wants: []string{"a model probe could not reach", "made no chat call"},
			unwants: []string{
				"cannot reach the model server", "host is down", "is unreachable",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ModelErrorLine("https://"+host+"/v1", tc.probe, false)
			for _, want := range tc.wants {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q: %q", want, got)
				}
			}
			for _, bad := range tc.unwants {
				if strings.Contains(got, bad) {
					t.Fatalf("claims %q for a probe: %q", bad, got)
				}
			}
			if !strings.Contains(got, host) {
				t.Fatalf("probe line does not name the host: %q", got)
			}
		})
	}
}

// TestChatDialErrorStillSaysCannotReach proves the cannot-reach wording is
// reserved for a failed chat call.
func TestChatDialErrorStillSaysCannotReach(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	got := ModelErrorLine("http://127.0.0.1:9/v1", dial, false)
	if !strings.Contains(got, "cannot reach the model server") {
		t.Fatalf("a failed chat call must keep the cannot-reach line: %q", got)
	}
}

// TestChatHTTPErrorIsNotAProbe proves a chat HTTP error keeps its own wording
// and never reads as a probe.
func TestChatHTTPErrorIsNotAProbe(t *testing.T) {
	err := &llm.HTTPError{Status: 401, Body: "invalid key"}
	if _, isProbe := llm.AsProbe(err); isProbe {
		t.Fatal("a chat HTTP error must not be a probe error")
	}
	got := ModelErrorLine("https://api.example.test/v1", err, false)
	if strings.Contains(got, "probe") || strings.Contains(got, "cannot reach") {
		t.Fatalf("chat error read as a probe: %q", got)
	}
	if !strings.Contains(got, "rejected the request") {
		t.Fatalf("chat 401 wording: %q", got)
	}
}

func TestProbeErrorsKeepTheirKindThroughAWrap(t *testing.T) {
	probe := &llm.ProbeError{Kind: llm.ProbeAuthFailed, Status: 403}
	wrapped := errors.Join(errors.New("llm: list models"), probe)
	if _, ok := llm.AsProbe(wrapped); !ok {
		t.Fatal("AsProbe must see a wrapped probe error")
	}
	got := ModelErrorLine("https://api.example.test/v1", wrapped, false)
	if !strings.Contains(got, "HTTP 403") || strings.Contains(got, "cannot reach") {
		t.Fatalf("wrapped probe wording: %q", got)
	}
}
