package commands

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSetModelReasoningEffort(t *testing.T) {
	env, out, errb := setEnv(t)
	if code := runSet(context.Background(), env, []string{"model", "reasoning", "default", "high"}); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if got := env.Profile.Models[env.Profile.Roles.Main].ReasoningEffort; got != "high" {
		t.Fatalf("model effort %q", got)
	}
	body, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "reasoning_effort: high") {
		t.Fatalf("file\n%s", body)
	}
	out.Reset()
	if got := formatModelTable(context.Background(), env); !strings.Contains(got, "effort high") {
		t.Fatalf("/set model does not show the option:\n%s", got)
	}
	errb.Reset()
	if code := runSet(context.Background(), env, []string{"model", "reasoning", "default", "maximum"}); code != 2 {
		t.Fatalf("invalid value code %d", code)
	}
	if !strings.Contains(errb.String(), "reasoning_effort must be") {
		t.Fatalf("stderr %q", errb.String())
	}
	if code := runSet(context.Background(), env, []string{"model", "reasoning", "default", "default"}); code != 0 {
		t.Fatal(errb.String())
	}
	if env.Profile.Models[env.Profile.Roles.Main].ReasoningEffort != "" {
		t.Fatal("default must clear the field")
	}
}
