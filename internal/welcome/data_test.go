package welcome

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/store"
)

func TestGatherListsOnlyConfiguredEndpoints(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()
	data, err := Gather(context.Background(), Input{
		Endpoint: up.URL + "/v1",
		Model:    "main-model",
		CWD:      t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Models) != 1 || data.Total != 1 || data.Models[0].Role != "main" {
		t.Fatalf("models %+v total %d", data.Models, data.Total)
	}
	if data.Host == "" {
		t.Fatal("missing host")
	}
}

func TestGatherHonorsProbeDeadline(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	start := time.Now()
	_, err := Gather(context.Background(), Input{
		Endpoint:    slow.URL + "/v1",
		Model:       "main-model",
		ProbeBudget: 80 * time.Millisecond,
		CWD:         t.TempDir(),
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("probe deadline not honored: %s", elapsed)
	}
}

func TestGatherProbesDeadlineAndOffline(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		case "/info":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	refused := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	refusedURL := refused.URL
	refused.Close()

	start := time.Now()
	data, err := Gather(context.Background(), Input{
		Endpoint:       up.URL + "/v1",
		Model:          "main-model",
		FastEndpoint:   slow.URL + "/v1",
		FastModel:      "mini",
		EmbedEndpoint:  refusedURL,
		RerankEndpoint: up.URL,
		CWD:            t.TempDir(),
		Version:        "v0.1.0-3-gabcdef1",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("gather took %s", elapsed)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("models path %q", gotPath)
	}
	if len(data.Models) != 4 || data.Total != 4 {
		t.Fatalf("models %+v total %d", data.Models, data.Total)
	}
	byRole := map[string]Model{}
	for _, m := range data.Models {
		byRole[m.Role] = m
	}
	if byRole["main"].Status != StatusUp || byRole["main"].Name != "main-model" {
		t.Fatalf("main %+v", byRole["main"])
	}
	if !strings.HasSuffix(byRole["main"].Stat, "ms") && !strings.Contains(byRole["main"].Stat, "tokens/s") {
		t.Fatalf("main stat %q", byRole["main"].Stat)
	}
	if byRole["fast"].Status != StatusTimeout || byRole["fast"].Stat != "timeout" {
		t.Fatalf("fast %+v", byRole["fast"])
	}
	if byRole["embed"].Status != StatusDown || !strings.Contains(byRole["embed"].Stat, "unreachable") {
		t.Fatalf("embed %+v", byRole["embed"])
	}
	if byRole["rerank"].Status != StatusUp || !strings.HasSuffix(byRole["rerank"].Stat, "ms") {
		t.Fatalf("rerank %+v", byRole["rerank"])
	}
	if data.Up != 2 {
		t.Fatalf("up %d", data.Up)
	}
	if data.Session.Memory != "memory offline" {
		t.Fatalf("memory %q", data.Session.Memory)
	}
	if len(data.Recent) != 0 {
		t.Fatalf("recent %+v", data.Recent)
	}
	if data.Placeholder != "ask anything" {
		t.Fatalf("placeholder %q", data.Placeholder)
	}
	if data.Tip == "" {
		t.Fatal("empty tip")
	}
	if data.Session.Branch != "not a git repo" {
		t.Fatalf("branch %q", data.Session.Branch)
	}
	if data.Session.Build != "v0.1.0  abcdef1" {
		t.Fatalf("build %q", data.Session.Build)
	}
	if data.Host == "" {
		t.Fatal("empty host")
	}
}

func TestStatusThresholds(t *testing.T) {
	if statusFor("main", 999*time.Millisecond) != StatusUp {
		t.Fatal("main up")
	}
	if statusFor("main", time.Second) != StatusSlow {
		t.Fatal("main slow")
	}
	if statusFor("embed", time.Second) != StatusUp {
		t.Fatal("embed at 1s is up")
	}
	if statusFor("embed", time.Second+time.Millisecond) != StatusSlow {
		t.Fatal("embed slow")
	}
}

func TestAgeAndPlaceholder(t *testing.T) {
	if ageLabel(5*time.Minute) != "5m" || ageLabel(2*time.Hour) != "2h" || ageLabel(24*time.Hour) != "1d" || ageLabel(21*24*time.Hour) != "3w" {
		t.Fatalf("%s %s %s %s", ageLabel(5*time.Minute), ageLabel(2*time.Hour), ageLabel(24*time.Hour), ageLabel(21*24*time.Hour))
	}
	now := time.Now()
	row := store.WelcomeRow{Title: "Fix the parser", CWD: "/work/app", LastAt: now.Add(-time.Hour)}
	if got := resumePlaceholder(row, true, "/work/app", now); got != "ask anything, or /resume to continue Fix the parser" {
		t.Fatalf("%q", got)
	}
	old := row
	old.LastAt = now.Add(-25 * time.Hour)
	if got := resumePlaceholder(old, true, "/work/app", now); got != "ask anything" {
		t.Fatalf("old %q", got)
	}
	if got := resumePlaceholder(row, true, "/other", now); got != "ask anything" {
		t.Fatalf("dir %q", got)
	}
	long := strings.Repeat("a", 60)
	if got := displayTitle("", long); !strings.HasSuffix(got, "...") || len([]rune(got)) != 48 {
		t.Fatalf("clip %q", got)
	}
	if tipFor(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)) == "" {
		t.Fatal("tip")
	}
}

func TestGatherGitBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "demo")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	nested := filepath.Join(home, "src", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	mv := exec.Command("git", "init", "-b", "demo")
	mv.Dir = nested
	if out, err := mv.CombinedOutput(); err != nil {
		t.Fatalf("init nested: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := Gather(context.Background(), Input{CWD: nested, Version: "v0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if data.Session.Branch != "demo" {
		t.Fatalf("branch %q", data.Session.Branch)
	}
	if !data.Session.Dirty || data.Session.Changed < 1 {
		t.Fatalf("dirty %v changed %d", data.Session.Dirty, data.Session.Changed)
	}
	if data.Session.Dir != "~/src/app" {
		t.Fatalf("dir %q", data.Session.Dir)
	}
	if data.Session.Memory != "memory offline" || len(data.Recent) != 0 {
		t.Fatalf("mem %q recent %d", data.Session.Memory, len(data.Recent))
	}
}
