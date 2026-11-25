package config

import (
	"strings"
	"testing"
)

func remoteProfile() Config {
	c := Builtin()
	c.Models["api"] = Model{Endpoint: "http://203.0.113.9/v1", APIKeyEnv: "KONSOLAI_TEST_KEY"}
	c.Roles.Main = "api"
	return c
}

func TestRemoteGatePolicyChecks(t *testing.T) {
	ResetRemoteState()
	t.Cleanup(ResetRemoteState)
	SetRemotePrompt(func(string) (bool, error) { return false, nil })
	t.Cleanup(func() { SetRemotePrompt(nil) })

	c := remoteProfile()
	c.Privacy.AllowRemote = false
	err := c.remoteCallError("main", "api", true)
	if err == nil || !strings.Contains(err.Error(), "privacy.allow_remote") || !strings.Contains(err.Error(), `"api"`) {
		t.Fatalf("allow_remote error: %v", err)
	}

	c = remoteProfile()
	c.Privacy.RemoteRoles = []string{"fast"}
	err = c.remoteCallError("main", "api", true)
	if err == nil || !strings.Contains(err.Error(), "privacy.remote_roles") {
		t.Fatalf("remote_roles error: %v", err)
	}

	c = remoteProfile()
	if err := c.remoteCallError("main", "api", true); err != nil {
		t.Fatalf("--yes should allow: %v", err)
	}

	ResetRemoteState()
	SetRemotePrompt(func(host string) (bool, error) {
		if !strings.Contains(host, "203.0.113.9") {
			t.Fatalf("prompt host %q", host)
		}
		return true, nil
	})
	c = remoteProfile()
	SetRemotePrompt(func(string) (bool, error) { return true, nil })
	if err := c.remoteCallError("main", "api", true); err != nil {
		t.Fatalf("assumeYes: %v", err)
	}
}

func TestRemoteGateTokenCap(t *testing.T) {
	ResetRemoteState()
	t.Cleanup(ResetRemoteState)
	c := remoteProfile()
	c.Privacy.SessionTokenCap = 10
	NoteRemoteUsage(11)
	err := c.remoteCallError("main", "api", true)
	if err == nil || !strings.Contains(err.Error(), "privacy.session_token_cap") {
		t.Fatalf("cap error: %v", err)
	}
}

func TestRemoteGateDeclinedIsRemembered(t *testing.T) {
	ResetRemoteState()
	t.Cleanup(ResetRemoteState)
	c := remoteProfile()
	c.Privacy.AllowRemote = true
	// Non-interactive test process: without --yes the call must be refused.
	err := c.remoteCallError("main", "api", false)
	if err == nil || !strings.Contains(err.Error(), "non-interactive") {
		t.Fatalf("non-interactive error: %v", err)
	}
}
