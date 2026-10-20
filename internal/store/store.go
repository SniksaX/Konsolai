package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/redact"
)

const (
	maxOutput     = 8 * 1024
	maxFullOutput = 256 * 1024
	maxBefore     = 1 << 20
)

// queryTimeout is the built-in query limit. Open stores the resolved value
// on Store; this name stays for callers that open a pool directly.
var queryTimeout = config.Builtin().StoreQueryTimeout()

type Store struct {
	pool           *pgxpool.Pool
	queryTimeout   time.Duration
	migrateTimeout time.Duration
}

// OpenOptions carries store deadlines. Zero values keep the built-in limits.
// SearchPath, when set, is a single schema name prepended to public on every
// connection. The schema must already exist.
type OpenOptions struct {
	QueryTimeout   time.Duration
	MigrateTimeout time.Duration
	SearchPath     string
}

type Session struct {
	ID             string
	Profile        string
	ProjectID      *int64
	CWD            string
	Mode           string
	Model          string
	Title          string
	Status         string
	StartedAt      time.Time
	LastActivity   time.Time
	EndedAt        *time.Time
	CompactedUpto  *int
	CompactSummary string
}

type NewSession struct {
	Profile   string
	ProjectID *int64
	CWD       string
	Mode      string
	Model     string
	Title     string
}

type Message struct {
	Seq           int
	Role          string
	Content       string
	ToolCalls     []byte
	ToolCallID    string
	TokenEstimate int
	CWD           string
}

type ActionIn struct {
	SessionID    string
	Seq          int
	Tool         string
	Args         []byte
	Kind         string
	Decision     string
	RejectReason string
	IsError      bool
	Meta         string
	Duration     time.Duration
	Output       string
	FullOutput   string
}

type FileChangeIn struct {
	SessionID     string
	Seq           int
	Path          string
	ExistedBefore bool
	Before        []byte
	AfterHash     string
	Diff          string
}

type FileChangeRow struct {
	ID            int64
	SessionID     string
	Seq           int
	Path          string
	ExistedBefore bool
	BeforeContent []byte
	BeforeHash    string
	AfterHash     string
	Diff          string
	UndoBlocked   string
}

type Fact struct {
	ID     int64
	Fact   string
	Source string
	Scope  string
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	return OpenWith(ctx, databaseURL, OpenOptions{})
}

func resolvedOpen(opt OpenOptions) (query, migrate time.Duration) {
	query = opt.QueryTimeout
	if query <= 0 {
		query = config.Builtin().StoreQueryTimeout()
	}
	migrate = opt.MigrateTimeout
	if migrate <= 0 {
		migrate = config.Builtin().StoreMigrateTimeout()
	}
	return query, migrate
}

func OpenWith(ctx context.Context, databaseURL string, opt OpenOptions) (*Store, error) {
	if err := validateSearchPath(opt.SearchPath); err != nil {
		return nil, err
	}
	query, migrate := resolvedOpen(opt)
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse config: %w", err)
	}
	if opt.SearchPath != "" {
		schema := opt.SearchPath
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET search_path TO "+pgx.Identifier{schema}.Sanitize()+", public")
			return err
		}
	}
	cfg.ConnConfig.ConnectTimeout = query
	connectCtx, cancel := context.WithTimeout(ctx, query)
	pool, err := pgxpool.NewWithConfig(connectCtx, cfg)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, query)
	err = pool.Ping(pingCtx)
	cancel()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	s := &Store{pool: pool, queryTimeout: query, migrateTimeout: migrate}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func validateSearchPath(schema string) error {
	if schema == "" {
		return nil
	}
	for i, r := range schema {
		letter := r >= 'a' && r <= 'z'
		digit := r >= '0' && r <= '9'
		if i == 0 && !letter {
			return fmt.Errorf("store: search path: %s", schema)
		}
		if !letter && !digit && r != '_' {
			return fmt.Errorf("store: search path: %s", schema)
		}
	}
	return nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}
}

func (s *Store) UpsertProject(ctx context.Context, root string, kinds, commands []string) (int64, error) {
	if kinds == nil {
		kinds = []string{}
	}
	if commands == nil {
		commands = []string{}
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (root, kinds, commands, last_seen)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (root) DO UPDATE
		SET kinds = EXCLUDED.kinds, commands = EXCLUDED.commands, last_seen = now()
		RETURNING id`, root, kinds, commands).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: upsert project: %w", err)
	}
	return id, nil
}

func (s *Store) FindResumableSession(ctx context.Context, profile string, projectID *int64, cwd, mode string, idle time.Duration) (Session, bool, error) {
	_ = mode
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	mins := int(idle / time.Minute)
	if mins < 1 {
		mins = 1
	}
	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, profile, project_id, cwd, mode, model, title, status,
		       started_at, last_activity_at, ended_at
		FROM sessions
		WHERE profile = $1
		  AND status IN ('active', 'finished', 'failed')
		  AND NOT closed
		  AND last_activity_at >= now() - make_interval(mins => $2)
		  AND (
		    ($3::bigint IS NOT NULL AND project_id = $3)
		    OR ($3::bigint IS NULL AND cwd = $4)
		  )
		ORDER BY last_activity_at DESC
		LIMIT 1`, profile, mins, projectID, cwd).Scan(
		&sess.ID, &sess.Profile, &sess.ProjectID, &sess.CWD, &sess.Mode, &sess.Model, &sess.Title, &sess.Status,
		&sess.StartedAt, &sess.LastActivity, &sess.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("store: find session: %w", err)
	}
	return sess, true, nil
}

type RecentSession struct {
	ID              string
	Title           string
	LastUserMessage string
	Age             time.Duration
}

func (s *Store) RecentSessions(ctx context.Context, profile string, projectID *int64, cwd string, idle time.Duration, limit int) ([]RecentSession, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	mins := int(idle / time.Minute)
	if mins < 1 {
		mins = 1
	}
	if limit < 1 {
		limit = 3
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id::text, s.title, s.last_activity_at,
		       COALESCE((
		         SELECT m.content FROM messages m
		         WHERE m.session_id = s.id AND m.role = 'user'
		         ORDER BY m.seq DESC LIMIT 1
		       ), '')
		FROM sessions s
		WHERE s.profile = $1
		  AND s.status IN ('active', 'finished', 'failed')
		  AND NOT s.closed
		  AND s.last_activity_at >= now() - make_interval(mins => $2)
		  AND (
		    ($3::bigint IS NOT NULL AND s.project_id = $3)
		    OR ($3::bigint IS NULL AND s.cwd = $4)
		  )
		ORDER BY s.last_activity_at DESC
		LIMIT $5`, profile, mins, projectID, cwd, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent sessions: %w", err)
	}
	defer rows.Close()
	var out []RecentSession
	now := time.Now()
	for rows.Next() {
		var r RecentSession
		var lastAt time.Time
		if err := rows.Scan(&r.ID, &r.Title, &lastAt, &r.LastUserMessage); err != nil {
			return nil, fmt.Errorf("store: scan recent session: %w", err)
		}
		r.Age = now.Sub(lastAt)
		if r.Age < 0 {
			r.Age = 0
		}
		r.LastUserMessage = cutRunes(r.LastUserMessage, 120)
		out = append(out, r)
	}
	return out, rows.Err()
}

func cutRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (s *Store) CreateSession(ctx context.Context, ns NewSession) (string, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (profile, project_id, cwd, mode, model, title, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'active')
		RETURNING id::text`,
		ns.Profile, ns.ProjectID, ns.CWD, ns.Mode, ns.Model, ns.Title).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("store: create session: %w", err)
	}
	return id, nil
}

func (s *Store) TouchSession(ctx context.Context, id string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET last_activity_at = now() WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("store: touch session: %w", err)
	}
	return nil
}

func (s *Store) EndSession(ctx context.Context, id, status string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET status = $2, ended_at = now(), last_activity_at = now()
		WHERE id = $1::uuid`, id, status)
	if err != nil {
		return fmt.Errorf("store: end session: %w", err)
	}
	return nil
}

func (s *Store) NextSeq(ctx context.Context, sessionID string) (int, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var seq int
	err := s.pool.QueryRow(ctx, `
		SELECT GREATEST(
			COALESCE((SELECT MAX(seq) FROM messages WHERE session_id = $1::uuid), 0),
			COALESCE((SELECT MAX(seq) FROM actions WHERE session_id = $1::uuid), 0),
			COALESCE((SELECT MAX(seq) FROM file_changes WHERE session_id = $1::uuid), 0)
		) + 1`, sessionID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("store: next seq: %w", err)
	}
	return seq, nil
}

func (s *Store) AppendMessage(ctx context.Context, sessionID string, seq int, m Message) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	content := redact.String(m.Content)
	var calls []byte
	if len(m.ToolCalls) > 0 {
		calls = redact.JSON(m.ToolCalls)
	}
	tok := m.TokenEstimate
	if tok == 0 {
		tok = (len(content) + len(calls)) / 4
	}
	var toolCallID *string
	if m.ToolCallID != "" {
		toolCallID = &m.ToolCallID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO messages (session_id, seq, role, content, tool_calls, tool_call_id, token_estimate, cwd)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`,
		sessionID, seq, m.Role, content, nullableJSON(calls), toolCallID, tok, m.CWD)
	if err != nil {
		return fmt.Errorf("store: append message: %w", err)
	}
	return nil
}

func (s *Store) SessionByID(ctx context.Context, id string) (Session, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, profile, project_id, cwd, mode, model, title, status,
		       started_at, last_activity_at, ended_at, compacted_upto, COALESCE(compact_summary, '')
		FROM sessions WHERE id = $1::uuid`, id).Scan(
		&sess.ID, &sess.Profile, &sess.ProjectID, &sess.CWD, &sess.Mode, &sess.Model, &sess.Title, &sess.Status,
		&sess.StartedAt, &sess.LastActivity, &sess.EndedAt, &sess.CompactedUpto, &sess.CompactSummary)
	if err != nil {
		return Session{}, fmt.Errorf("store: session by id: %w", err)
	}
	return sess, nil
}

func (s *Store) SetCompact(ctx context.Context, id string, upto int, summary string) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET compacted_upto = $2, compact_summary = $3, last_activity_at = now()
		WHERE id = $1::uuid`, id, upto, summary)
	if err != nil {
		return fmt.Errorf("store: set compact: %w", err)
	}
	return nil
}

func (s *Store) LoadMessages(ctx context.Context, sessionID string) ([]Message, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT seq, role, content, tool_calls, COALESCE(tool_call_id, ''), token_estimate, COALESCE(cwd, '')
		FROM messages WHERE session_id = $1::uuid ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: load messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Seq, &m.Role, &m.Content, &m.ToolCalls, &m.ToolCallID, &m.TokenEstimate, &m.CWD); err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RecordAction(ctx context.Context, a ActionIn) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	args := a.Args
	if len(args) == 0 {
		args = []byte("{}")
	}
	args = redact.JSON(args)
	out := clipString(redact.String(a.Output), maxOutput)
	full := a.FullOutput
	if full == "" {
		full = a.Output
	}
	full = clipString(redact.String(full), maxFullOutput)
	var reason *string
	if a.RejectReason != "" {
		reason = &a.RejectReason
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO actions (session_id, seq, tool, args, kind, decision, reject_reason, is_error, meta, duration_ms, output, full_output)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		a.SessionID, a.Seq, a.Tool, args, a.Kind, a.Decision, reason, a.IsError, a.Meta, int(a.Duration/time.Millisecond), out, full)
	if err != nil {
		return fmt.Errorf("store: record action: %w", err)
	}
	return nil
}

func (s *Store) RecordFileChange(ctx context.Context, c FileChangeIn) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	diffText := redact.String(c.Diff)
	var before []byte
	var beforeHash *string
	var blocked *string
	switch {
	case redact.SensitivePath(c.Path):
		reason := "sensitive file"
		blocked = &reason
	case c.ExistedBefore && len(c.Before) > maxBefore:
		reason := "too large"
		blocked = &reason
	case c.ExistedBefore:
		before = c.Before
		h := sha256Hex(c.Before)
		beforeHash = &h
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO file_changes (session_id, seq, path, existed_before, before_content, before_hash, after_hash, diff, undo_blocked)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9)`,
		c.SessionID, c.Seq, c.Path, c.ExistedBefore, before, beforeHash, c.AfterHash, diffText, blocked)
	if err != nil {
		return fmt.Errorf("store: record file change: %w", err)
	}
	return nil
}

func (s *Store) UndoCandidates(ctx context.Context, projectID *int64, cwd string) ([]FileChangeRow, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.session_id::text, f.seq, f.path, f.existed_before, f.before_content,
		       COALESCE(f.before_hash, ''), f.after_hash, f.diff, COALESCE(f.undo_blocked, '')
		FROM file_changes f
		WHERE f.reverted_at IS NULL
		  AND f.session_id = (
		    SELECT s.id FROM sessions s
		    WHERE (
		      ($1::bigint IS NOT NULL AND s.project_id = $1)
		      OR ($1::bigint IS NULL AND s.cwd = $2)
		    )
		    AND EXISTS (
		      SELECT 1 FROM file_changes x
		      WHERE x.session_id = s.id AND x.reverted_at IS NULL
		    )
		    ORDER BY s.last_activity_at DESC
		    LIMIT 1
		  )
		ORDER BY f.seq DESC`, projectID, cwd)
	if err != nil {
		return nil, fmt.Errorf("store: undo candidates: %w", err)
	}
	defer rows.Close()
	var out []FileChangeRow
	for rows.Next() {
		var r FileChangeRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Seq, &r.Path, &r.ExistedBefore, &r.BeforeContent, &r.BeforeHash, &r.AfterHash, &r.Diff, &r.UndoBlocked); err != nil {
			return nil, fmt.Errorf("store: scan file change: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkReverted(ctx context.Context, id int64) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	_, err := s.pool.Exec(ctx, `UPDATE file_changes SET reverted_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: mark reverted: %w", err)
	}
	return nil
}

func (s *Store) ListSessions(ctx context.Context, profile string, limit int) ([]Session, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, profile, project_id, cwd, mode, model, title, status,
		       started_at, last_activity_at, ended_at
		FROM sessions WHERE profile = $1
		ORDER BY last_activity_at DESC
		LIMIT $2`, profile, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.Profile, &sess.ProjectID, &sess.CWD, &sess.Mode, &sess.Model, &sess.Title, &sess.Status,
			&sess.StartedAt, &sess.LastActivity, &sess.EndedAt); err != nil {
			return nil, fmt.Errorf("store: scan session: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *Store) AddUserFact(ctx context.Context, profile, fact, source string) (int64, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_facts (profile, fact, source)
		VALUES ($1, $2, $3)
		ON CONFLICT (profile, fact) DO UPDATE SET source = EXCLUDED.source
		RETURNING id`, profile, fact, source).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: add user fact: %w", err)
	}
	return id, nil
}

func (s *Store) AddProjectFact(ctx context.Context, projectID int64, fact string, sourceSession *string) (int64, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO project_facts (project_id, fact, source_session)
		VALUES ($1, $2, $3::uuid)
		ON CONFLICT (project_id, fact) DO UPDATE SET fact = EXCLUDED.fact
		RETURNING id`, projectID, fact, sourceSession).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: add project fact: %w", err)
	}
	return id, nil
}

func (s *Store) ListFacts(ctx context.Context, profile string, projectID *int64) (user, project []Fact, err error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT id, fact, source FROM user_facts WHERE profile = $1 ORDER BY id`, profile)
	if err != nil {
		return nil, nil, fmt.Errorf("store: list user facts: %w", err)
	}
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.Fact, &f.Source); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("store: scan user fact: %w", err)
		}
		f.Scope = "user"
		user = append(user, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if projectID == nil {
		return user, nil, nil
	}
	rows, err = s.pool.Query(ctx, `
		SELECT id, fact, 'explicit' FROM project_facts WHERE project_id = $1 ORDER BY id`, *projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: list project facts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.Fact, &f.Source); err != nil {
			return nil, nil, fmt.Errorf("store: scan project fact: %w", err)
		}
		f.Scope = "project"
		project = append(project, f)
	}
	return user, project, rows.Err()
}

func (s *Store) DeleteFact(ctx context.Context, scope string, id int64) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var q string
	switch scope {
	case "user":
		q = `DELETE FROM user_facts WHERE id = $1`
	case "project":
		q = `DELETE FROM project_facts WHERE id = $1`
	default:
		return fmt.Errorf("store: delete fact: unknown scope %q", scope)
	}
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("store: delete fact: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: delete fact: id %d not found", id)
	}
	return nil
}

func (s *Store) GetWebCache(ctx context.Context, url string) (title, text string, ok bool, err error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	err = s.pool.QueryRow(ctx, `
		SELECT title, text FROM web_cache WHERE url = $1 AND expires_at > now()`, url).Scan(&title, &text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("store: get web cache: %w", err)
	}
	return title, text, true, nil
}

func (s *Store) PutWebCache(ctx context.Context, url, title, text string, ttl time.Duration) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	secs := int(ttl / time.Second)
	if secs < 1 {
		secs = 1
	}
	sum := sha256Hex([]byte(text))
	_, err := s.pool.Exec(ctx, `
		INSERT INTO web_cache (url, title, text, content_hash, fetched_at, expires_at)
		VALUES ($1, $2, $3, $4, now(), now() + make_interval(secs => $5))
		ON CONFLICT (url) DO UPDATE SET
			title = EXCLUDED.title,
			text = EXCLUDED.text,
			content_hash = EXCLUDED.content_hash,
			fetched_at = EXCLUDED.fetched_at,
			expires_at = EXCLUDED.expires_at`, url, title, text, sum, secs)
	if err != nil {
		return fmt.Errorf("store: put web cache: %w", err)
	}
	return nil
}

func (s *Store) PruneIfDue(ctx context.Context, retentionDays int) error {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var last string
	err := s.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = 'last_prune'`).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("store: last prune: %w", err)
	}
	if last != "" {
		t, perr := time.Parse(time.RFC3339Nano, last)
		if perr != nil {
			t, perr = time.Parse(time.RFC3339, last)
		}
		if perr == nil && time.Since(t) < 24*time.Hour {
			return nil
		}
	}
	if retentionDays < 1 {
		retentionDays = 90
	}
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM sessions WHERE last_activity_at < now() - make_interval(days => $1)`, retentionDays); err != nil {
		return fmt.Errorf("store: prune sessions: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM web_cache WHERE expires_at < now()`); err != nil {
		return fmt.Errorf("store: prune web cache: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO meta (key, value) VALUES ('last_prune', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, now)
	if err != nil {
		return fmt.Errorf("store: record prune: %w", err)
	}
	return nil
}

// ListedSession is one row of the numbered session list.
type ListedSession struct {
	Session
	FirstUser string
	Intent    string
}

// Here reports whether sess belongs to the current project, or to cwd when
// there is no project.
func Here(projectID *int64, cwd string, sess Session) bool {
	if projectID != nil {
		return sess.ProjectID != nil && *sess.ProjectID == *projectID
	}
	return sess.CWD == cwd
}

// NumberedSessions is the list for /history, /resume, /show, /diff and the
// welcome RECENT panel. Sessions of the current project (or cwd, when there
// is no project) come first, then every other open session, newest first
// inside each group. hereOnly keeps the current project.
func (s *Store) NumberedSessions(ctx context.Context, profile string, projectID *int64, cwd string, limit int, hereOnly bool) ([]ListedSession, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("store: list sessions: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	if limit < 1 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id::text, s.profile, s.project_id, s.cwd, s.mode, s.model, s.title, s.status,
		       s.started_at, s.last_activity_at, s.ended_at,
		       COALESCE((
		         SELECT m.content FROM messages m
		         WHERE m.session_id = s.id AND m.role = 'user'
		         ORDER BY m.seq ASC
		         LIMIT 1
		       ), ''),
		       COALESCE((
		         SELECT t.intent FROM turn_stats t
		         WHERE t.session_id = s.id
		         ORDER BY t.created_at DESC, t.id DESC
		         LIMIT 1
		       ), s.mode)
		FROM sessions s
		WHERE s.profile = $1
		  AND NOT s.closed
		  AND (
		    NOT $4::bool
		    OR ($2::bigint IS NOT NULL AND s.project_id = $2)
		    OR ($2::bigint IS NULL AND s.cwd = $3)
		  )
		ORDER BY (
		  CASE
		    WHEN $2::bigint IS NOT NULL AND s.project_id = $2 THEN 0
		    WHEN $2::bigint IS NULL AND s.cwd = $3 THEN 0
		    ELSE 1
		  END
		), s.last_activity_at DESC
		LIMIT $5`, profile, projectID, cwd, hereOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()
	var out []ListedSession
	for rows.Next() {
		var row ListedSession
		if err := rows.Scan(&row.ID, &row.Profile, &row.ProjectID, &row.CWD, &row.Mode, &row.Model, &row.Title, &row.Status,
			&row.StartedAt, &row.LastActivity, &row.EndedAt, &row.FirstUser, &row.Intent); err != nil {
			return nil, fmt.Errorf("store: scan list session: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	return out, nil
}

func (s *Store) HistorySessions(ctx context.Context, profile string, projectID *int64, cwd string, limit int) ([]Session, error) {
	rows, err := s.NumberedSessions(ctx, profile, projectID, cwd, limit, true)
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, row := range rows {
		out = append(out, row.Session)
	}
	return out, nil
}

func (s *Store) CloseSessions(ctx context.Context, profile string, projectID *int64, cwd string) (int64, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET closed = true
		WHERE profile = $1
		  AND NOT closed
		  AND (
		    ($2::bigint IS NOT NULL AND project_id = $2)
		    OR ($2::bigint IS NULL AND cwd = $3)
		  )`, profile, projectID, cwd)
	if err != nil {
		return 0, fmt.Errorf("store: close sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var v int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}

type ActionRow struct {
	Seq    int
	Tool   string
	Args   []byte
	Kind   string
	Output string
}

func (s *Store) ListActions(ctx context.Context, sessionID string) ([]ActionRow, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT seq, tool, args, kind, output FROM actions
		WHERE session_id = $1::uuid ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: list actions: %w", err)
	}
	defer rows.Close()
	var out []ActionRow
	for rows.Next() {
		var a ActionRow
		if err := rows.Scan(&a.Seq, &a.Tool, &a.Args, &a.Kind, &a.Output); err != nil {
			return nil, fmt.Errorf("store: scan action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListFileChanges(ctx context.Context, sessionID string) ([]FileChangeRow, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id::text, seq, path, existed_before, COALESCE(before_content, ''::bytea),
		       COALESCE(before_hash, ''), after_hash, diff, COALESCE(undo_blocked, '')
		FROM file_changes WHERE session_id = $1::uuid AND reverted_at IS NULL
		ORDER BY seq`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: list file changes: %w", err)
	}
	defer rows.Close()
	var out []FileChangeRow
	for rows.Next() {
		var c FileChangeRow
		if err := rows.Scan(&c.ID, &c.SessionID, &c.Seq, &c.Path, &c.ExistedBefore, &c.BeforeContent, &c.BeforeHash, &c.AfterHash, &c.Diff, &c.UndoBlocked); err != nil {
			return nil, fmt.Errorf("store: scan file change: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) RecentShellOutputs(ctx context.Context, profile string, limit int) ([]string, error) {
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	if limit < 1 {
		limit = 1
	}
	if limit > 5 {
		limit = 5
	}
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(full_output, ''), output)
		FROM actions a
		JOIN sessions s ON s.id = a.session_id
		WHERE s.profile = $1 AND a.tool = 'run_shell'
		ORDER BY a.created_at DESC, a.seq DESC
		LIMIT $2`, profile, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent shell outputs: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("store: scan shell output: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func clipString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func (s *Store) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	d := time.Duration(0)
	if s != nil {
		d = s.queryTimeout
	}
	if d <= 0 {
		d = config.Builtin().StoreQueryTimeout()
	}
	return context.WithTimeout(ctx, d)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func nullableJSON(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
