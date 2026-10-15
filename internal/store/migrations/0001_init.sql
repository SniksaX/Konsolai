CREATE TABLE projects (
  id bigserial PRIMARY KEY,
  root text UNIQUE NOT NULL,
  kinds text[] NOT NULL DEFAULT '{}',
  commands text[] NOT NULL DEFAULT '{}',
  last_seen timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  profile text NOT NULL,
  project_id bigint REFERENCES projects ON DELETE SET NULL,
  cwd text NOT NULL,
  mode text NOT NULL CHECK (mode IN ('howto', 'do', 'code')),
  model text NOT NULL,
  title text NOT NULL,
  status text NOT NULL CHECK (status IN ('active', 'finished', 'aborted', 'failed')),
  started_at timestamptz NOT NULL DEFAULT now(),
  last_activity_at timestamptz NOT NULL DEFAULT now(),
  ended_at timestamptz
);

CREATE INDEX sessions_profile_cwd_activity ON sessions (profile, cwd, last_activity_at DESC);
CREATE INDEX sessions_project_activity ON sessions (project_id, last_activity_at DESC);

CREATE TABLE messages (
  session_id uuid REFERENCES sessions ON DELETE CASCADE,
  seq int NOT NULL,
  role text NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
  content text NOT NULL,
  tool_calls jsonb,
  tool_call_id text,
  token_estimate int NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, seq)
);

CREATE TABLE actions (
  id bigserial PRIMARY KEY,
  session_id uuid REFERENCES sessions ON DELETE CASCADE,
  seq int NOT NULL,
  tool text NOT NULL,
  args jsonb NOT NULL,
  kind text NOT NULL,
  decision text NOT NULL CHECK (decision IN
    ('approved', 'approved_all', 'auto', 'rejected', 'gate_blocked', 'invalid')),
  reject_reason text,
  is_error bool NOT NULL,
  meta text NOT NULL DEFAULT '',
  duration_ms int NOT NULL,
  output text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_facts (
  id bigserial PRIMARY KEY,
  profile text NOT NULL,
  fact text NOT NULL,
  source text NOT NULL CHECK (source IN ('explicit', 'inferred')),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (profile, fact)
);

CREATE TABLE project_facts (
  id bigserial PRIMARY KEY,
  project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
  fact text NOT NULL,
  source_session uuid REFERENCES sessions ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project_id, fact)
);

CREATE TABLE file_changes (
  id bigserial PRIMARY KEY,
  session_id uuid REFERENCES sessions ON DELETE CASCADE,
  seq int NOT NULL,
  path text NOT NULL,
  existed_before bool NOT NULL,
  before_content bytea,
  before_hash text,
  after_hash text NOT NULL,
  diff text NOT NULL,
  undo_blocked text,
  reverted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE web_cache (
  url text PRIMARY KEY,
  title text NOT NULL,
  text text NOT NULL,
  content_hash text NOT NULL,
  fetched_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL
);

CREATE TABLE meta (
  key text PRIMARY KEY,
  value text NOT NULL
);
