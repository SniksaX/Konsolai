package config

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func stubLookup(t *testing.T, m map[string][]net.IP) {
	t.Helper()
	prev := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		if ips, ok := m[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIP = prev })
	remoteMu.Lock()
	remoteCache = map[string]bool{}
	remoteMu.Unlock()
}

func TestHostRemoteClassification(t *testing.T) {
	stubLookup(t, map[string][]net.IP{
		"gpu.lan":  {net.ParseIP("10.1.1.143")},
		"api.pub":  {net.ParseIP("203.0.113.9")},
		"mixed.io": {net.ParseIP("10.0.0.2"), net.ParseIP("198.51.100.7")},
	})
	cases := []struct {
		host string
		want bool
	}{
		{"", false},
		{"localhost", false},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.1.1.143", false},
		{"192.168.1.2", false},
		{"gpu.lan", false},
		{"8.8.8.8", true},
		{"api.pub", true},
		{"mixed.io", true},
		{"unknown.example", true}, // unresolved counts as remote
	}
	for _, c := range cases {
		if got := HostRemote(c.host); got != c.want {
			t.Errorf("%q: got %v want %v", c.host, got, c.want)
		}
	}
}

func TestRemoteModelAndEmbedRejection(t *testing.T) {
	stubLookup(t, map[string][]net.IP{"api.pub": {net.ParseIP("203.0.113.9")}})
	m := Model{Endpoint: "https://api.pub/v1"}
	if !m.Remote() {
		t.Fatal("public model should be remote")
	}
	if (Model{Endpoint: "http://localhost:8000/v1"}).Remote() {
		t.Fatal("localhost is not remote")
	}

	c := Builtin()
	c.Roles.Embed = "https://api.pub/v1"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "roles.embed") {
		t.Fatalf("remote embed must be rejected: %v", err)
	}
	c.Roles.Embed = "http://10.1.1.143:9503"
	if err := Validate(c); err != nil {
		t.Fatalf("private embed: %v", err)
	}
	c.Roles.Embed = "http://embed-unresolved:9"
	if err := Validate(c); err != nil {
		t.Fatalf("an unresolved container name is allowed at validation: %v", err)
	}
}

func TestPrivacyDefaultsAndRoles(t *testing.T) {
	c := Builtin()
	if !c.Privacy.AllowRemote || c.Privacy.SessionTokenCap != 0 {
		t.Fatalf("privacy defaults %+v", c.Privacy)
	}
	if !c.RemoteRoleAllowed("main") || !c.RemoteRoleAllowed("fast") || c.RemoteRoleAllowed("embed") {
		t.Fatalf("remote roles %v", c.Privacy.RemoteRoles)
	}
}
