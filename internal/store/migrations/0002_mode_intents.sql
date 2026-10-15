ALTER TABLE sessions DROP CONSTRAINT IF EXISTS sessions_mode_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_mode_check
  CHECK (mode IN ('howto','do','code','answer','lookup','act','build'));
