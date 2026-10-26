-- Account self-service columns on users (docs/tickets/06-accounts.md, 資料表 users).
-- Idempotent: every change checks for prior application.

ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS locale VARCHAR(10) NOT NULL DEFAULT 'zh-TW';
ALTER TABLE users ADD COLUMN IF NOT EXISTS ui_theme VARCHAR(10) NOT NULL DEFAULT 'system';
ALTER TABLE users ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'active';
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_email VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_email_hash CHAR(64) NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_email_expires_at TIMESTAMPTZ;

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'author';

DO $$
BEGIN
    ALTER TABLE users ADD CONSTRAINT users_status_check
        CHECK (status IN ('active', 'invited', 'disabled'));
EXCEPTION WHEN duplicate_object THEN
    NULL;
END;
$$;

DO $$
BEGIN
    ALTER TABLE users ADD CONSTRAINT users_ui_theme_check
        CHECK (ui_theme IN ('system', 'light', 'dark'));
EXCEPTION WHEN duplicate_object THEN
    NULL;
END;
$$;
