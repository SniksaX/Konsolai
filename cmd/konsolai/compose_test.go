package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestDockerScriptDoesNotRemoveContainers(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(moduleRoot(t), "scripts", "docker.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "docker rm") {
		t.Fatal("scripts/docker.sh must not remove containers directly")
	}
	if !strings.Contains(string(body), `KONSOLAI_UID="$(id -u)"`) || !strings.Contains(string(body), `KONSOLAI_GID="$(id -g)"`) {
		t.Fatal("scripts/docker.sh must export the host uid and gid")
	}
}

func TestComposeServiceUser(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(moduleRoot(t), "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `user: "${KONSOLAI_UID:-1000}:${KONSOLAI_GID:-1000}"`) {
		t.Fatal("konsolai service must run as the host uid and gid")
	}
}

func TestComposeConfig(t *testing.T) {
	requireDocker(t)
	root := moduleRoot(t)
	hideDotEnv(t, root)
	pass := randToken(t)
	cmd := exec.Command("docker", "compose", "-f", filepath.Join(root, "compose.yaml"), "config", "-q")
	cmd.Dir = root
	cmd.Env = composeEnv(root, pass, 55434, t.TempDir(), filepath.Join(t.TempDir(), "unused.yaml"), "konsolai-config-check", "konsolai-config-check-embed")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compose config: %v\n%s", err, scrub(string(out), pass))
	}
}

func TestComposeFileDefaults(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(moduleRoot(t), "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, ":?") {
		t.Fatal("compose.yaml uses required-variable syntax")
	}
	url := "postgres://postgres:${KONSOLAI_DB_PASSWORD:-konsolai}@db:5432/konsolai?sslmode=disable"
	if !strings.Contains(text, url) {
		t.Fatal("compose database URL is not the default")
	}
	for _, line := range strings.Split(config.ComposeVarTable(), "\n") {
		if !strings.HasPrefix(line, "| `KONSOLAI_") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			t.Fatalf("row %q", line)
		}
		name := strings.Trim(cells[1], " `")
		def := strings.Trim(cells[2], " `")
		needle := "${" + name + ":-" + def + "}"
		if !strings.Contains(text, needle) {
			t.Errorf("compose.yaml missing %s", needle)
		}
	}
}

func TestComposeConfigNoDotEnv(t *testing.T) {
	requireDocker(t)
	root := moduleRoot(t)
	hideDotEnv(t, root)
	bare := []string{"PATH=" + os.Getenv("PATH")}
	quiet := exec.Command("docker", "compose", "-f", filepath.Join(root, "compose.yaml"), "config", "-q")
	quiet.Dir = root
	quiet.Env = bare
	if out, err := quiet.CombinedOutput(); err != nil {
		t.Fatalf("compose config -q: %v\n%s", err, out)
	}
	cmd := exec.Command("docker", "compose", "--profile", "app", "-f", filepath.Join(root, "compose.yaml"), "config")
	cmd.Dir = root
	cmd.Env = bare
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compose config: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{
		"POSTGRES_PASSWORD: konsolai",
		"postgres://postgres:konsolai@db:5432/konsolai?sslmode=disable",
		`published: "55434"`,
		"target: 5432",
		"user: 1000:1000",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("resolved config missing %q", want)
		}
	}
}

func TestComposeDatabaseRoundTrip(t *testing.T) {
	requireDocker(t)
	root := moduleRoot(t)
	hideDotEnv(t, root)
	token := randToken(t)
	project := "konsolai-test-" + token[:12]
	if strings.Contains(project, "konsolai-pg") {
		t.Fatalf("project %s", project)
	}
	pass := randToken(t)
	port := freePort(t)
	work := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("defaults: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	vol := project + "-db"
	env := composeEnv(root, pass, port, work, cfg, vol, project+"-embed")
	foreign := dockerRunning("konsolai-pg")

	run := func(ctx context.Context, args ...string) (string, error) {
		all := append([]string{"compose", "-p", project, "-f", filepath.Join(root, "compose.yaml")}, args...)
		cmd := exec.CommandContext(ctx, "docker", all...)
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return scrub(string(out), pass), err
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, err := run(ctx, "down", "-v", "--remove-orphans")
		if err != nil {
			t.Errorf("compose down: %v\n%s", err, out)
		}
		if foreign && !dockerRunning("konsolai-pg") {
			t.Error("the developer's database container stopped")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "docker", "build", "--target", "base", "-t", "konsolai:dev", root)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, scrub(string(out), pass))
	}

	if out, err := run(ctx, "up", "-d", "db"); err != nil {
		t.Fatalf("db up: %v\n%s", err, out)
	}
	if err := waitHealthy(ctx, project, "db"); err != nil {
		out, _ := run(ctx, "logs", "db")
		t.Fatalf("db health: %v\n%s", err, scrub(out, pass))
	}

	out, err := run(ctx, "--profile", "app", "run", "--rm", "-T", "konsolai", "/status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	dbLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "database") {
			dbLine = line
		}
	}
	if !schemaVersion.MatchString(dbLine) || strings.Contains(dbLine, "unknown") || strings.Contains(dbLine, "cannot connect") || strings.Contains(dbLine, "not configured") {
		t.Fatalf("database status:\n%s", out)
	}
}

var schemaVersion = regexp.MustCompile(`schema [1-9][0-9]*`)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("needs docker")
	}
	if !dockerDaemon(t) {
		t.Skip("docker daemon is not available")
	}
}

func dockerDaemon(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func dockerRunning(name string) bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func composeEnv(root, pass string, port int, work, cfg, dbVol, embedVol string) []string {
	env := os.Environ()
	set := map[string]string{
		"KONSOLAI_DB_PASSWORD":  pass,
		"KONSOLAI_DB_PORT":      fmt.Sprintf("%d", port),
		"KONSOLAI_TARGET":       "base",
		"KONSOLAI_IMAGE":        "konsolai:dev",
		"KONSOLAI_VERSION":      "docker",
		"KONSOLAI_WORKDIR":      work,
		"KONSOLAI_CONFIG_FILE":  cfg,
		"KONSOLAI_DB_VOLUME":    dbVol,
		"KONSOLAI_EMBED_VOLUME": embedVol,
		"KONSOLAI_EMBED_MODEL":  "BAAI/bge-base-en-v1.5",
		"KONSOLAI_EMBED_PORT":   "8080",
		"KONSOLAI_UID":          fmt.Sprintf("%d", os.Getuid()),
		"KONSOLAI_GID":          fmt.Sprintf("%d", os.Getgid()),
	}
	var out []string
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if _, ok := set[k]; ok {
			continue
		}
		if k == "COMPOSE_PROJECT_NAME" || k == "COMPOSE_FILE" {
			continue
		}
		out = append(out, e)
	}
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	_ = root
	return out
}

func randToken(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func freePort(t *testing.T) int {
	t.Helper()
	for p := 55434; p < 55500; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return p
	}
	t.Fatal("no free database port")
	return 0
}

func waitHealthy(ctx context.Context, project, service string) error {
	deadline := time.Now().Add(90 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		idCmd := exec.CommandContext(ctx, "docker", "ps", "-q",
			"--filter", "label=com.docker.compose.project="+project,
			"--filter", "label=com.docker.compose.service="+service,
		)
		id, err := idCmd.Output()
		cid := strings.TrimSpace(string(id))
		if i := strings.IndexByte(cid, '\n'); i >= 0 {
			cid = cid[:i]
		}
		if err == nil && cid != "" {
			inspect := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{if .State.Health}}{{.State.Health.Status}}{{end}}", cid)
			out, ierr := inspect.Output()
			last = strings.TrimSpace(string(out))
			if ierr == nil && last == "healthy" {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("not healthy (%s)", last)
}

func hideDotEnv(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".env")
	if _, err := os.Stat(path); err != nil {
		return
	}
	aside := path + ".held"
	if err := os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(aside, path); err != nil {
			t.Errorf("restore .env: %v", err)
		}
	})
}

func scrub(s, pass string) string {
	if pass == "" {
		return s
	}
	return strings.ReplaceAll(s, pass, "redacted")
}
