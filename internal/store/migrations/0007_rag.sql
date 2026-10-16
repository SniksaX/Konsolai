CREATE OR REPLACE FUNCTION unaccent_immutable(txt text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$ SELECT unaccent('unaccent', txt) $$;

CREATE TABLE memory_chunks (
  id bigserial PRIMARY KEY,
  profile text NOT NULL,
  project_id bigint REFERENCES projects ON DELETE CASCADE,
  source text NOT NULL CHECK (source IN ('turn', 'summary', 'web', 'code')),
  ref jsonb NOT NULL,
  content text NOT NULL,
  content_hash text NOT NULL,
  embedding vector(__DIM__) NOT NULL,
  tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', unaccent_immutable(content))) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (profile, source, content_hash)
);

CREATE INDEX memory_chunks_embedding_hnsw ON memory_chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX memory_chunks_tsv_gin ON memory_chunks USING gin (tsv);
CREATE INDEX memory_chunks_scope ON memory_chunks (profile, project_id, source);

CREATE TABLE memory_files (
  project_id bigint NOT NULL REFERENCES projects ON DELETE CASCADE,
  path text NOT NULL,
  sha256 text NOT NULL,
  indexed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, path)
);
