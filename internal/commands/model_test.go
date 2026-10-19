package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/welcome"
)

func TestSuggestModelName(t *testing.T) {
	long := strings.Repeat("a", 30)
	cases := []struct{ in, want string }{
		{"Acme-3-3B-Instruct-Q4_K_M.gguf", "acme-3-3b-instruct"},
		{"Vendor-NVFP4", "vendor"},
		{"Plain-AWQ", "plain"},
		{"Plain-GPTQ", "plain"},
		{"Name.gguf", "name"},
		{"Foo.Q5_K_M", "foo"},
		{"/opt/weights/box-model-Q8_0.gguf", "box-model"},
		{"data/models/box-model", "box-model"},
		{`data\models\box-model`, "box-model"},
		{"Some.Name.v2", "some-name-v2"},
		{"Box-Model-FP16", "box-model"},
		{"IQ4_XS", "model"},
		{long, strings.Repeat("a", 24)},
	}
	for _, tc := range cases {
		if got := SuggestModelName(tc.in); got != tc.want {
			t.Errorf("SuggestModelName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProbeRoleMethods(t *testing.T) {
	var mainMethod, embedMethod, rerankMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
			mainMethod = r.Method
			fmt.Fprint(w, `{"data":[{"id":"box-model","max_model_len":4096}]}`)
		case r.URL.Path == "/embed" && r.Method == http.MethodPost:
			embedMethod = r.Method
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/info" && r.Method == http.MethodGet:
			rerankMethod = r.Method
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		fmt.Fprint(w, `{"data":[{"id":"slow-model","n_ctx":2048}]}`)
	}))
	defer slow.Close()

	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer hang.Close()

	env := &Env{Profile: config.Builtin(), Out: nil}
	env.Profile.Models = map[string]config.Model{
		"default": {Endpoint: srv.URL + "/v1"},
	}
	env.Profile.Roles.Main = "default"
	env.Profile.Roles.Fast = ""
	env.Profile.Roles.Embed = srv.URL
	env.Profile.Roles.Rerank = srv.URL
	env.Profile.UI.ProbeTimeoutMS = 500
	env.Profile.UI.ModelSlowMS = 1000
	out := formatModelTable(context.Background(), env)
	if mainMethod != http.MethodGet || embedMethod != http.MethodPost || rerankMethod != http.MethodGet {
		t.Fatalf("methods main=%s embed=%s rerank=%s", mainMethod, embedMethod, rerankMethod)
	}
	for _, role := range []string{"main", "embed", "rerank"} {
		if !strings.Contains(out, role) || !strings.Contains(out, "up") {
			t.Fatalf("missing up %s\n%s", role, out)
		}
	}

	status, _ := welcome.Probe(context.Background(), down.URL+"/v1", "main", "models", 300*time.Millisecond, time.Second)
	if status != "down" {
		t.Fatalf("down status %s", status)
	}
	status, _ = welcome.Probe(context.Background(), slow.URL+"/v1", "main", "models", time.Second, 30*time.Millisecond)
	if status != "slow" {
		t.Fatalf("slow status %s", status)
	}
	status, _ = welcome.Probe(context.Background(), slow.URL+"/v1", "embed", "embed", time.Second, 30*time.Millisecond)
	if status != "slow" {
		t.Fatalf("embed slow status %s", status)
	}
	status, _ = welcome.Probe(context.Background(), hang.URL+"/v1", "main", "models", 40*time.Millisecond, time.Second)
	if status != "timeout" {
		t.Fatalf("timeout status %s", status)
	}
}

func TestServerReportedIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"box-model","max_model_len":4096}]}`)
	}))
	defer srv.Close()
	env := &Env{Profile: config.Builtin()}
	env.Profile.Models = map[string]config.Model{
		"default": {Endpoint: srv.URL + "/v1"},
	}
	env.Profile.Roles.Main = "default"
	env.Profile.Roles.Embed = ""
	env.Profile.Roles.Rerank = ""
	env.Profile.UI.ProbeTimeoutMS = 500
	env.Profile.UI.ModelSlowMS = 1000
	out := formatModelTable(context.Background(), env)
	for _, want := range []string{"box-model (server)", "ctx 4096 (server)", "up"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q\n%s", want, out)
		}
	}
	// A later probe failure still shows the cached server id and context.
	env.Profile.Models["default"] = config.Model{Endpoint: srv.URL + "/v1"}
	dead := "http://127.0.0.1:1/v1"
	env.rememberServer(dead, seenModel{ID: "cached-id", Context: 111})
	env.Profile.Models["default"] = config.Model{Endpoint: dead}
	env.Profile.UI.ProbeTimeoutMS = 40
	out = formatModelTable(context.Background(), env)
	for _, want := range []string{"cached-id (server)", "ctx 111 (server)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("cache missing %q\n%s", want, out)
		}
	}
}

func TestDiscoverAlreadyConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"box-model-Q4_K_M.gguf"},{"id":"other-model"}]}`)
	}))
	defer srv.Close()
	c := config.Builtin()
	c.Models = map[string]config.Model{
		"default": {Endpoint: srv.URL + "/v1", Model: "box-model-Q4_K_M.gguf"},
	}
	c.Roles.Main = "default"
	c.UI.ProbeTimeoutMS = 500
	out := discoverModels(context.Background(), c)
	if !strings.Contains(out, "already configured as default") {
		t.Fatalf("missing already configured\n%s", out)
	}
	if strings.Contains(out, "/set model add box-model ") {
		t.Fatalf("suggested an add for a configured id\n%s", out)
	}
	if !strings.Contains(out, "/set model add other-model ") {
		t.Fatalf("missing suggestion\n%s", out)
	}
	if strings.Contains(out, "data-models-") {
		t.Fatalf("path fragment in %s", out)
	}
}
