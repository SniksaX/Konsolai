package welcome

import (
	"strings"
	"testing"
)

// TestRenderMCPConfiguredUntilConnected is the lazy-connect welcome rule: before
// any server connects the line reads "N configured", not "0/N up"; after a
// connection it reads "up/total up".
func TestRenderMCPConfiguredUntilConnected(t *testing.T) {
	plain(t)
	cases := []struct {
		name    string
		count   MCPCount
		want    string
		strip   string
		notWant string
	}{
		{"one configured", MCPCount{Up: 0, Total: 1}, "1 configured", "mcp 1 configured", "0/1 up"},
		{"many configured", MCPCount{Up: 0, Total: 4}, "4 configured", "mcp 4 configured", "0/4 up"},
		{"all up", MCPCount{Up: 1, Total: 1}, "1/1 up", "mcp 1/1 up", "configured"},
		{"partly up", MCPCount{Up: 2, Total: 4}, "2/4 up", "mcp 2/4 up", "configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := allUp()
			d.MCP = tc.count
			for _, layout := range []string{"split", "frame"} {
				got := renderString(d, layout, 140, 40)
				if !strings.Contains(got, tc.want) {
					t.Fatalf("%s misses %q:\n%s", layout, tc.want, got)
				}
				if strings.Contains(got, tc.notWant) {
					t.Fatalf("%s still shows %q:\n%s", layout, tc.notWant, got)
				}
			}
			// The degraded layout paints the same count under the status strip.
			if got := renderString(d, "split", 140, 20); !strings.Contains(got, tc.strip) {
				t.Fatalf("degraded layout misses %q:\n%s", tc.strip, got)
			}
		})
	}
}

// TestRenderMCPConfiguredFlip proves the same screen flips once a server
// connects: nothing about the layout is cached.
func TestRenderMCPConfiguredFlip(t *testing.T) {
	plain(t)
	d := allUp()
	d.MCP = MCPCount{Up: 0, Total: 2}
	before := renderString(d, "split", 140, 40)
	if !strings.Contains(before, "2 configured") {
		t.Fatalf("before connect:\n%s", before)
	}
	d.MCP = MCPCount{Up: 1, Total: 2}
	after := renderString(d, "split", 140, 40)
	if !strings.Contains(after, "1/2 up") || strings.Contains(after, "configured") {
		t.Fatalf("after connect:\n%s", after)
	}
}

// TestRenderMCPHiddenWithoutServers keeps the line out of a profile with no
// configured server.
func TestRenderMCPHiddenWithoutServers(t *testing.T) {
	plain(t)
	d := allUp()
	d.MCP = MCPCount{}
	for _, layout := range []string{"split", "frame", "minimal"} {
		if got := renderString(d, layout, 140, 40); strings.Contains(got, "mcp") {
			t.Fatalf("%s paints an MCP line with no server configured:\n%s", layout, got)
		}
	}
}
