-- Signed-in devices (docs/tickets/06-accounts.md, 資料表 login_sessions).
-- id is hashLoginID(jti), the same value stored in revoked_logins.id; the raw jti is never stored.

CREATE TABLE IF NOT EXISTS login_sessions (
    id CHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method VARCHAR(32) NOT NULL DEFAULT 'password',
    device_label VARCHAR(120) NOT NULL DEFAULT '',
    ua_hash CHAR(64) NOT NULL DEFAULT '',
    ip_masked VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_login_sessions_user_id ON login_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_login_sessions_expires_at ON login_sessions(expires_at);
