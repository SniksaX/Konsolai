package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/embed"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/rag"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestEmbedDimMismatchDisablesRAG(t *testing.T) {
	raw := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	rag.ResetDims()
	t.Cleanup(rag.ResetDims)

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var tok [6]byte
	if _, err := rand.Read(tok[:]); err != nil {
		t.Fatal(err)
	}
	schema := "ktdim_" + hex.EncodeToString(tok[:])
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
	})
	publicBefore := schemaVectorWidth(t, admin, "public")

	st, err := store.OpenWith(ctx, raw, store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if name, err := st.CurrentSchema(context.Background()); err == nil && name == schema {
			_ = st.ResetEmbeddingDim(context.Background(), store.EmbeddingDim)
		}
		rag.ResetDims()
		st.Close()
	})
	if got, err := st.CurrentSchema(ctx); err != nil || got != schema {
		t.Fatalf("schema %q err=%v", got, err)
	}

	if got, err := st.EmbeddingDim(ctx); err != nil || got != store.EmbeddingDim {
		t.Fatalf("index dim %d err=%v", got, err)
	}
	if schemaVectorWidth(t, admin, schema) != store.EmbeddingDim {
		t.Fatalf("catalog width %d", schemaVectorWidth(t, admin, schema))
	}

	var calls atomic.Int32
	vec := make([]float32, 768)
	vec[0] = 1
	embSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode([][]float32{vec})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(embSrv.Close)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": "hello there"}}},
		})
		_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(llmSrv.Close)

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()
	var notice, errB strings.Builder
	var memErr error
	emb := embed.New(embSrv.URL, "")
	ing := rag.New(st, emb, "default")
	ing.SetNotice(&notice)
	prof := config.Builtin()
	prof.Name = "default"
	prof.RAG.Enabled = true
	prof.Roles.Embed = embSrv.URL
	prof.SetMain(llmSrv.URL+"/v1", "test", false)
	prof.Database.URL = raw
	runner := &turn.Runner{
		Profile: prof,
		Client:  llm.New(llmSrv.URL+"/v1", "test"),
		Mem:     memory.NewGuard(st, func(err error) { memErr = err }),
		Out:     io.Discard,
		Err:     &errB,
		Ingest:  ing,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return &summaryConf{d: ui.Approve}, func() {}, nil
		},
	}
	res := runner.RunTurn(ctx, turn.Request{Text: "hi", Intent: "answer", Yes: true, Cwd: cwd, New: true})
	if res.ExitCode != 0 || !strings.Contains(res.Answer, "hello") {
		t.Fatalf("exit %d answer %q err=%s mem=%v", res.ExitCode, res.Answer, errB.String(), memErr)
	}
	if res.SessionID == "" {
		t.Fatal("missing session")
	}
	ing.Enqueue(rag.Item{Source: "turn", Content: "second chunk"})
	if n := ing.Flush(ctx, time.Second); n != 0 {
		t.Fatalf("dropped %d", n)
	}
	line := rag.MismatchLine(768, store.EmbeddingDim)
	if strings.Count(notice.String(), "rag disabled:") != 1 || !strings.Contains(notice.String(), line) {
		t.Fatalf("notice %q calls=%d", notice.String(), calls.Load())
	}
	if calls.Load() != 1 {
		t.Fatalf("embed calls %d", calls.Load())
	}
	if n := chunkTotal(t, st, "default"); n != 0 {
		t.Fatalf("inserted %d chunks", n)
	}
	if _, err := st.AddUserFact(ctx, "default", "keep me", "explicit"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordTurnStat(ctx, store.TurnStat{
		SessionID: res.SessionID, Intent: "answer", Model: "dim-model",
		Endpoint: "http://example", PromptTokens: 1, CompletionTokens: 2, ModelMS: 4,
	}); err != nil {
		t.Fatal(err)
	}

	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	home := t.TempDir()
	doctorHome = func() (string, error) { return home, nil }
	cfgPath := home + "/config.yaml"
	if err := os.WriteFile(cfgPath, []byte("profiles:\n  default: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env, out, errb := testEnv(t)
	env.Profile = prof
	env.Mem = memory.NewGuard(st, nil)
	env.Embed = emb
	env.ConfigPath = cfgPath
	env.Cwd = cwd
	env.Runner = runner
	hit := checkEmbedDims(ctx, env)
	if hit.mark != "!" || hit.detail != line {
		t.Fatalf("doctor %+v", hit)
	}
	if code := runDoctor(ctx, env, nil); code != 0 || !strings.Contains(out.String(), line) {
		t.Fatalf("doctor exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	out.Reset()
	if code := runStatus(ctx, env, nil); code != 0 {
		t.Fatalf("status %d\n%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "embed dims     768 (index 1024)") {
		t.Fatalf("status\n%s", out.String())
	}

	ask := &summaryConf{d: ui.Reject}
	env.Confirm = ask
	out.Reset()
	errb.Reset()
	if code := runSet(ctx, env, []string{"rag", "reset-dim"}); code == 0 {
		t.Fatal("decline succeeded")
	}
	if !strings.Contains(ask.summary, "drops and recreates memory_chunks and memory_files") ||
		!strings.Contains(ask.summary, "current model's dimension") ||
		!strings.Contains(ask.summary, "Indexed data is lost.") {
		t.Fatalf("warning %q", ask.summary)
	}
	if !strings.Contains(out.String(), "left unchanged") {
		t.Fatalf("decline %q", out.String())
	}
	if got, err := st.EmbeddingDim(ctx); err != nil || got != 1024 {
		t.Fatalf("dim after decline %d %v", got, err)
	}

	out.Reset()
	errb.Reset()
	if code := runSet(ctx, env, []string{"rag", "reset-dim", "--yes"}); code != 0 {
		t.Fatalf("reset %d %s", code, errb.String())
	}
	if got, err := st.EmbeddingDim(ctx); err != nil || got != 768 {
		t.Fatalf("dim after reset %d %v", got, err)
	}
	if schemaVectorWidth(t, admin, schema) != 768 {
		t.Fatalf("catalog %d", schemaVectorWidth(t, admin, schema))
	}
	sess, ok, err := st.FindResumableSession(ctx, "default", nil, cwd, "answer", time.Hour)
	if err != nil || !ok || sess.ID != res.SessionID {
		t.Fatalf("session ok=%v id=%s err=%v", ok, sess.ID, err)
	}
	users, _, err := st.ListFacts(ctx, "default", nil)
	if err != nil || len(users) != 1 || users[0].Fact != "keep me" {
		t.Fatalf("facts %+v %v", users, err)
	}
	rate, err := st.ModelRate(ctx, "dim-model", 5)
	if err != nil || rate == nil {
		t.Fatalf("turn_stats %v %v", rate, err)
	}

	ing.Enqueue(rag.Item{Source: "turn", Content: "third chunk"})
	if n := ing.Flush(ctx, time.Second); n != 0 {
		t.Fatalf("dropped %d", n)
	}
	if strings.Count(notice.String(), "rag disabled:") != 1 {
		t.Fatalf("repeated notice %q", notice.String())
	}
	if n := chunkTotal(t, st, "default"); n != 1 {
		t.Fatalf("chunks after match %d", n)
	}
	out.Reset()
	hit = checkEmbedDims(ctx, env)
	if hit.mark != "✓" || hit.detail != "ok" {
		t.Fatalf("doctor after reset %+v", hit)
	}
	if code := runStatus(ctx, env, nil); code != 0 || !strings.Contains(out.String(), "embed dims     768 (index 768)") {
		t.Fatalf("status after reset\n%s", out.String())
	}

	if err := st.ResetEmbeddingDim(ctx, store.EmbeddingDim); err != nil {
		t.Fatal(err)
	}
	rag.ResetDims()
	if got, err := st.EmbeddingDim(ctx); err != nil || got != store.EmbeddingDim {
		t.Fatalf("restored %d %v", got, err)
	}
	if schemaVectorWidth(t, admin, schema) != store.EmbeddingDim {
		t.Fatalf("catalog restored %d", schemaVectorWidth(t, admin, schema))
	}
	if got := schemaVectorWidth(t, admin, "public"); got != publicBefore {
		t.Fatalf("public width %d want %d", got, publicBefore)
	}
}

func TestDoctorEmbedDimsNotConfigured(t *testing.T) {
	rag.ResetDims()
	t.Cleanup(rag.ResetDims)
	env, _, _ := testEnv(t)
	hit := checkEmbedDims(context.Background(), env)
	if hit.mark != "!" || hit.detail != "not configured" {
		t.Fatalf("%+v", hit)
	}
	var out strings.Builder
	env.Out = &out
	writeEmbedDims(context.Background(), env)
	if out.Len() != 0 {
		t.Fatalf("status wrote %q", out.String())
	}
}

func chunkTotal(t *testing.T, st *store.Store, profile string) int {
	t.Helper()
	counts, err := st.ChunkCounts(context.Background(), profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func schemaVectorWidth(t *testing.T, admin *pgxpool.Pool, schema string) int {
	t.Helper()
	var typmod int
	var formatted string
	err := admin.QueryRow(context.Background(), `
		SELECT a.atttypmod, format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_type t ON t.oid = a.atttypid
		WHERE n.nspname = $1
		  AND c.relname = 'memory_chunks'
		  AND a.attname = 'embedding'
		  AND NOT a.attisdropped
		  AND t.typname = 'vector'`, schema).Scan(&typmod, &formatted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return -1
		}
		t.Fatal(err)
	}
	const marker = "vector("
	if i := strings.LastIndex(formatted, marker); i >= 0 {
		rest := formatted[i+len(marker):]
		if j := strings.IndexByte(rest, ')'); j > 0 {
			n := 0
			for _, c := range rest[:j] {
				if c < '0' || c > '9' {
					t.Fatalf("format %q", formatted)
				}
				n = n*10 + int(c-'0')
			}
			if n > 0 {
				return n
			}
		}
	}
	if typmod > 0 {
		return typmod
	}
	t.Fatalf("format %q typmod %d", formatted, typmod)
	return 0
}
