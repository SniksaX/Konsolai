package mcp

import (
	"errors"
	"strings"
	"testing"
)

// Fixtures are assembled at runtime so secret scanners never flag a literal.
const mcpLeakClientEnv = "KONSOLAI_MCP_CLIENT_LEAK_SECRET"

func mcpClientLeakSecret() string {
	return "sk" + "-" + "3f8a1c6e9d2b7045" + "a7c4e2f1"
}

// TestMaskErrorDropsResolvedHeaderValues is the transport-error surface: the
// guarded HTTP client rewrites an error that carries a resolved header value.
func TestMaskErrorDropsResolvedHeaderValues(t *testing.T) {
	secret := mcpClientLeakSecret()
	err := maskError(errors.New("dial failed for Authorization: Bearer "+secret), []string{secret})
	if err == nil {
		t.Fatal("maskError returned nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("maskError kept the header secret: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("maskError did not mark the value: %q", err.Error())
	}
}

// TestPlainHTTPHeaderErrorNamesHeadersOnly refuses to send a header over plain
// HTTP to a non-local host and names the header, never its value.
func TestPlainHTTPHeaderErrorNamesHeadersOnly(t *testing.T) {
	t.Setenv(mcpLeakClientEnv, mcpClientLeakSecret())
	err := plainHTTPHeaderError("web", parseURLOrNil("http://203.0.113.9/mcp"), map[string]string{
		"Authorization": "Bearer ${" + mcpLeakClientEnv + "}",
	})
	if err == nil {
		t.Fatal("a plain HTTP header to a non-local host was allowed")
	}
	if strings.Contains(err.Error(), mcpClientLeakSecret()) {
		t.Fatalf("the refusal carried the header value: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "Authorization") || !strings.Contains(err.Error(), "203.0.113.9") {
		t.Fatalf("the refusal misses the header name or host: %q", err.Error())
	}
}

// TestChildEnvResolvesAndMissingRefNamesVariableOnly proves the stdio child
// really receives the resolved env secret while an unset reference reports the
// variable name, never another value.
func TestChildEnvResolvesAndMissingRefNamesVariableOnly(t *testing.T) {
	secret := mcpClientLeakSecret()
	t.Setenv(mcpLeakClientEnv, secret)
	env, err := childEnv(map[string]string{"LEAK_TOKEN": "${" + mcpLeakClientEnv + "}"})
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, "LEAK_TOKEN=") {
			found = entry
		}
	}
	if found != "LEAK_TOKEN="+secret {
		t.Fatalf("the child environment misses the resolved secret: %q", found)
	}
	_, err = childEnv(map[string]string{"LEAK_TOKEN": "${KONSOLAI_MCP_CLIENT_MISSING}"})
	if err == nil {
		t.Fatal("an unset reference did not fail")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the env error carried a value: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "KONSOLAI_MCP_CLIENT_MISSING") {
		t.Fatalf("the env error misses the variable name: %q", err.Error())
	}
}

// TestSpecValidationNeverEchoesValues: a bad spec names the field, not the
// configured env or header value.
func TestSpecValidationNeverEchoesValues(t *testing.T) {
	secret := mcpClientLeakSecret()
	spec := Spec{
		Name:      "web",
		Transport: TransportHTTP,
		URL:       "http://127.0.0.1:9/mcp",
		Env:       map[string]string{"LEAK_TOKEN": secret},
	}
	err := spec.validate()
	if err == nil {
		t.Fatal("an HTTP spec with env was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation echoed the env value: %q", err.Error())
	}
}
