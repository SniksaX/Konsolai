package config

import (
	"net"
	"strings"
	"testing"
)

// roleGateProfile is a mixed configuration: roles.main points at a public test
// address and roles.fast at loopback, and roles.embed stays on loopback.
func roleGateProfile() Config {
	c := Builtin()
	c.Models["remote"] = Model{Endpoint: "http://203.0.113.9/v1", Model: "m"}
	c.Models["local"] = Model{Endpoint: "http://127.0.0.1:8000/v1", Model: "l"}
	c.Roles.Main = "remote"
	c.Roles.Fast = "remote"
	c.Roles.Embed = "http://127.0.0.1:9503"
	return c
}

func mustRemoteRefusal(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the remote call was not refused")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q is missing %q", err, want)
		}
	}
}

func TestAllowRemoteOffBlocksEveryRemoteRole(t *testing.T) {
	ResetRemoteState()
	t.Cleanup(ResetRemoteState)

	c := roleGateProfile()
	c.Privacy.AllowRemote = false
	for _, role := range []string{"main", "fast"} {
		mustRemoteRefusal(t, c.RemoteCallPolicy(role, "remote", true)(),
			"privacy.allow_remote", `"remote"`, "203.0.113.9")
	}
	// The role list never reopens a remote call when allow_remote is off.
	c.Privacy.RemoteRoles = []string{"main", "fast"}
	for _, role := range []string{"main", "fast"} {
		mustRemoteRefusal(t, c.RemoteCallPolicy(role, "remote", true)(), "privacy.allow_remote")
	}
	// Loopback models are not gated by the remote policy at all.
	for _, role := range []string{"main", "fast"} {
		if err := c.RemoteCallPolicy(role, "local", false)(); err != nil {
			t.Fatalf("role %q local model was gated: %v", role, err)
		}
	}
	if err := c.RemoteCallPolicy("main", "missing", true)(); err != nil {
		t.Fatalf("an unknown model was gated: %v", err)
	}
}

func TestRemoteRolesBlockExactlyTheUnlistedRole(t *testing.T) {
	ResetRemoteState()
	t.Cleanup(ResetRemoteState)

	c := roleGateProfile()
	c.Privacy.AllowRemote = true

	c.Privacy.RemoteRoles = []string{"main"}
	if err := c.RemoteCallPolicy("main", "remote", true)(); err != nil {
		t.Fatalf("the listed role was refused: %v", err)
	}
	mustRemoteRefusal(t, c.RemoteCallPolicy("fast", "remote", true)(),
		"privacy.remote_roles", `"fast"`, `"remote"`, "203.0.113.9")

	ResetRemoteState()
	c.Privacy.RemoteRoles = []string{"fast"}
	if err := c.RemoteCallPolicy("fast", "remote", true)(); err != nil {
		t.Fatalf("the listed role was refused: %v", err)
	}
	mustRemoteRefusal(t, c.RemoteCallPolicy("main", "remote", true)(),
		"privacy.remote_roles", `"main"`)

	// The list is compared case-insensitively and ignores surrounding space.
	ResetRemoteState()
	c.Privacy.RemoteRoles = []string{" Main "}
	if err := c.RemoteCallPolicy("main", "remote", true)(); err != nil {
		t.Fatalf("a spaced, capitalized role was refused: %v", err)
	}

	// A local role is never blocked by the list, listed or not.
	ResetRemoteState()
	for _, role := range []string{"main", "fast"} {
		if err := c.RemoteCallPolicy(role, "local", false)(); err != nil {
			t.Fatalf("role %q local model was gated: %v", role, err)
		}
	}
}

func TestMixedRolesValidateAcceptsRemoteChatWithLocalEmbed(t *testing.T) {
	// A remote chat model is allowed; only the embedding service is kept local.
	c := roleGateProfile()
	c.Privacy.AllowRemote = true
	c.Privacy.RemoteRoles = []string{"main"}
	if err := Validate(c); err != nil {
		t.Fatalf("mixed roles should validate: %v", err)
	}

	stubLookup(t, map[string][]net.IP{"embed.pub": {net.ParseIP("203.0.113.9")}})
	c.Roles.Embed = "https://embed.pub/v1"
	err := Validate(c)
	if err == nil {
		t.Fatal("a remote embed endpoint was accepted")
	}
	want := "roles.embed must be a loopback or private endpoint; a remote embedding service is not supported"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("remote embed message %q is missing %q", err, want)
	}
}
