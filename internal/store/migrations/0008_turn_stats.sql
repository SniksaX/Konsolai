CREATE TABLE turn_stats (
  id bigserial PRIMARY KEY,
  session_id uuid REFERENCES sessions ON DELETE CASCADE,
  intent text NOT NULL,
  model text NOT NULL,
  endpoint text NOT NULL,
  prompt_tokens int NOT NULL,
  completion_tokens int NOT NULL,
  model_ms int NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX turn_stats_model_created ON turn_stats (model, created_at DESC);
