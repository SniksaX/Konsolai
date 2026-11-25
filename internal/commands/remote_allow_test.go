package commands

import (
	"context"
	"strings"
	"testing"
)

func TestRemoteAllowResetsTheDecline(t *testing.T) {
	env, out, errb := testEnv(t)
	if code := runRemote(context.Background(), env, []string{"allow"}); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "remote prompts are enabled again") {
		t.Fatalf("stdout %q", out.String())
	}
	errb.Reset()
	if code := runRemote(context.Background(), env, nil); code != 2 {
		t.Fatalf("usage code %d", code)
	}
	if !strings.Contains(errb.String(), "usage: /remote allow") {
		t.Fatalf("stderr %q", errb.String())
	}
}
