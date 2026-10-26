-- Records which embedded migrations have been applied. The runner backfills
-- 0001-0015 the first time it sees this table and refuses to start when the
-- database is newer than the binary.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    name TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
