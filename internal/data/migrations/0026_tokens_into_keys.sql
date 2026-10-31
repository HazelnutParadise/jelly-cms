-- Ticket 22 (docs/tickets/22-api-keys-consolidation.md, 資料：遷移 0026).
-- Every personal API token (api_tokens) becomes one agent and one agent key.
-- Idempotent: a token whose hash already exists in agent_keys is skipped, so a
-- rerun adds nothing. api_tokens is kept; the program stops reading it and a
-- later migration removes it. The runner applies this file in one transaction.

ALTER TABLE agents
    ADD COLUMN IF NOT EXISTS role_cap VARCHAR(16);

ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_role_cap_check;
ALTER TABLE agents
    ADD CONSTRAINT agents_role_cap_check CHECK (
        role_cap IS NULL OR role_cap IN ('admin', 'editor', 'author')
    );

ALTER TABLE agent_keys DROP CONSTRAINT IF EXISTS agent_keys_prefix_check;
ALTER TABLE agent_keys
    ADD CONSTRAINT agent_keys_prefix_check CHECK (
        prefix ~ '^jla_[A-Za-z0-9_-]{8}$' OR prefix ~ '^jly_[A-Za-z0-9_-]{4,8}$'
    );

DO $$
DECLARE
    tok RECORD;
    scope_name TEXT;
    perms TEXT[];
    cap TEXT;
    author_perms TEXT[] := ARRAY['posts.read', 'posts.write', 'pages.read', 'media.read', 'media.write'];
    all_perms TEXT[] := ARRAY[
        'posts.read', 'posts.write', 'pages.read', 'pages.write',
        'media.read', 'media.write', 'products.read', 'products.write',
        'orders.read', 'themes.read', 'themes.write', 'plugins.read',
        'plugins.write', 'settings.read', 'settings.write', 'logs.read'
    ];
    candidate TEXT;
    base_name TEXT;
    suffix TEXT;
    counter INTEGER;
    new_agent_id BIGINT;
BEGIN
    FOR tok IN
        SELECT t.id, t.user_id, t.name, t.note, t.token_hash, t.last4, t.scopes,
               t.expires_at, t.revoked_at, t.last_used_at, t.last_used_ip_masked,
               t.created_at, u.username
        FROM api_tokens t
        JOIN users u ON u.id = t.user_id
        WHERE NOT EXISTS (SELECT 1 FROM agent_keys k WHERE k.key_hash = t.token_hash)
        ORDER BY t.id
    LOOP
        -- Agent name: the token name; on a case-insensitive clash with an active
        -- agent, "name (username)"; still clashing, " #2", " #3", ... Every
        -- result is capped at 80 characters.
        candidate := left(tok.name, 80);
        IF btrim(candidate) = '' THEN
            candidate := 'token ' || tok.id;
        END IF;
        IF EXISTS (SELECT 1 FROM agents WHERE deleted_at IS NULL AND lower(name) = lower(candidate)) THEN
            suffix := ' (' || tok.username || ')';
            candidate := left(left(tok.name, GREATEST(0, 80 - char_length(suffix))) || suffix, 80);
        END IF;
        base_name := candidate;
        counter := 1;
        WHILE EXISTS (SELECT 1 FROM agents WHERE deleted_at IS NULL AND lower(name) = lower(candidate)) LOOP
            counter := counter + 1;
            candidate := left(base_name, GREATEST(0, 80 - char_length(' #' || counter))) || ' #' || counter;
        END LOOP;

        -- Permissions from the scope mapping, de-duplicated and sorted. Unknown
        -- scopes are ignored.
        perms := ARRAY[]::TEXT[];
        FOREACH scope_name IN ARRAY tok.scopes LOOP
            IF scope_name = 'content_read' THEN
                perms := perms || ARRAY['posts.read', 'pages.read'];
            ELSIF scope_name = 'orders_read' THEN
                perms := perms || ARRAY['orders.read'];
            ELSIF scope_name = 'author' THEN
                perms := perms || author_perms;
            ELSIF scope_name = 'editor' THEN
                perms := perms || author_perms || ARRAY['pages.write'];
            ELSIF scope_name = 'admin' THEN
                perms := perms || all_perms;
            END IF;
        END LOOP;
        SELECT coalesce(array_agg(DISTINCT x ORDER BY x), ARRAY[]::TEXT[]) INTO perms
        FROM unnest(perms) AS x;

        -- Role cap: the highest of admin, editor and author in the scopes. A
        -- token with only content_read or orders_read has no cap.
        cap := CASE
            WHEN 'admin' = ANY(tok.scopes) THEN 'admin'
            WHEN 'editor' = ANY(tok.scopes) THEN 'editor'
            WHEN 'author' = ANY(tok.scopes) THEN 'author'
            ELSE NULL
        END;

        INSERT INTO agents (name, description, permissions, owner_id, created_by, status, role_cap, created_at, updated_at)
        VALUES (candidate, tok.note, perms, tok.user_id, tok.user_id, 'active', cap, tok.created_at, tok.created_at)
        RETURNING id INTO new_agent_id;

        INSERT INTO agent_keys (agent_id, key_hash, prefix, created_at, created_by, last_used_at,
                                last_used_ip_masked, expires_at, revoked_at, revoked_by)
        VALUES (new_agent_id, tok.token_hash, 'jly_' || btrim(tok.last4), tok.created_at, tok.user_id, tok.last_used_at,
                tok.last_used_ip_masked, tok.expires_at, tok.revoked_at, NULL);
    END LOOP;
END
$$;
