-- Ticket 25a (docs/tickets/25-integration-internal-rename.md): rename the
-- remaining agent internals to integration without changing the public API.
-- The runner applies this file in one transaction. Every step is guarded so a
-- direct rerun is a no-op.

DO $do$
BEGIN
    IF to_regclass('public.agents') IS NOT NULL THEN
        ALTER TABLE agents RENAME TO integrations;
    END IF;
    IF to_regclass('public.agent_keys') IS NOT NULL THEN
        ALTER TABLE agent_keys RENAME TO integration_keys;
    END IF;
    IF to_regclass('public.agent_events') IS NOT NULL THEN
        ALTER TABLE agent_events RENAME TO integration_events;
    END IF;
END
$do$;

DO $do$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = 'integration_keys' AND column_name = 'agent_id') THEN
        ALTER TABLE integration_keys RENAME COLUMN agent_id TO integration_id;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = 'integration_events' AND column_name = 'agent_id') THEN
        ALTER TABLE integration_events RENAME COLUMN agent_id TO integration_id;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema() AND table_name = 'integration_events' AND column_name = 'agent_name') THEN
        ALTER TABLE integration_events RENAME COLUMN agent_name TO integration_name;
    END IF;
END
$do$;

DO $do$
DECLARE
    item RECORD;
BEGIN
    FOR item IN
        SELECT * FROM (VALUES
            ('integrations', 'agents_pkey', 'integrations_pkey'),
            ('integrations', 'agents_owner_id_fkey', 'integrations_owner_id_fkey'),
            ('integrations', 'agents_created_by_fkey', 'integrations_created_by_fkey'),
            ('integrations', 'agents_status_check', 'integrations_status_check'),
            ('integrations', 'agents_permissions_check', 'integrations_permissions_check'),
            ('integrations', 'agents_role_cap_check', 'integrations_role_cap_check'),
            ('integration_keys', 'agent_keys_pkey', 'integration_keys_pkey'),
            ('integration_keys', 'agent_keys_agent_id_fkey', 'integration_keys_integration_id_fkey'),
            ('integration_keys', 'agent_keys_created_by_fkey', 'integration_keys_created_by_fkey'),
            ('integration_keys', 'agent_keys_revoked_by_fkey', 'integration_keys_revoked_by_fkey'),
            ('integration_keys', 'agent_keys_hash_check', 'integration_keys_hash_check'),
            ('integration_keys', 'agent_keys_prefix_check', 'integration_keys_prefix_check'),
            ('integration_keys', 'agent_keys_hash_key', 'integration_keys_hash_key'),
            ('integration_events', 'agent_events_pkey', 'integration_events_pkey'),
            ('integration_events', 'agent_events_actor_user_id_fkey', 'integration_events_actor_user_id_fkey'),
            ('integration_events', 'agent_events_agent_id_fkey', 'integration_events_integration_id_fkey'),
            ('integration_events', 'agent_events_summary_size_check', 'integration_events_summary_size_check')
        ) AS t(tbl, old_name, new_name)
    LOOP
        IF EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = to_regclass(item.tbl) AND conname = item.old_name) THEN
            EXECUTE format('ALTER TABLE %I RENAME CONSTRAINT %I TO %I', item.tbl, item.old_name, item.new_name);
        END IF;
    END LOOP;
END
$do$;

DO $do$
DECLARE
    item RECORD;
BEGIN
    FOR item IN
        SELECT * FROM (VALUES
            ('idx_agents_active_lower_name', 'idx_integrations_active_lower_name'),
            ('idx_agents_owner_id', 'idx_integrations_owner_id'),
            ('idx_agents_created_by', 'idx_integrations_created_by'),
            ('idx_agent_keys_agent_id', 'idx_integration_keys_integration_id'),
            ('idx_agent_keys_created_by', 'idx_integration_keys_created_by'),
            ('idx_agent_keys_revoked_by', 'idx_integration_keys_revoked_by'),
            ('idx_agent_events_agent_id_id', 'idx_integration_events_integration_id_id'),
            ('idx_agent_events_at', 'idx_integration_events_at'),
            ('idx_agent_events_actor_user_id', 'idx_integration_events_actor_user_id'),
            ('agents_pkey', 'integrations_pkey'),
            ('agent_keys_pkey', 'integration_keys_pkey'),
            ('agent_keys_hash_key', 'integration_keys_hash_key'),
            ('agent_events_pkey', 'integration_events_pkey')
        ) AS t(old_name, new_name)
    LOOP
        IF to_regclass('public.' || item.old_name) IS NOT NULL THEN
            EXECUTE format('ALTER INDEX public.%I RENAME TO %I', item.old_name, item.new_name);
        END IF;
    END LOOP;
END
$do$;

DO $do$
DECLARE
    seq_name TEXT;
BEGIN
    seq_name := pg_get_serial_sequence('integrations', 'id');
    IF seq_name IS NOT NULL AND seq_name <> 'public.integrations_id_seq' THEN
        EXECUTE format('ALTER SEQUENCE %s RENAME TO %I', seq_name, 'integrations_id_seq');
    END IF;

    seq_name := pg_get_serial_sequence('integration_keys', 'id');
    IF seq_name IS NOT NULL AND seq_name <> 'public.integration_keys_id_seq' THEN
        EXECUTE format('ALTER SEQUENCE %s RENAME TO %I', seq_name, 'integration_keys_id_seq');
    END IF;

    seq_name := pg_get_serial_sequence('integration_events', 'id');
    IF seq_name IS NOT NULL AND seq_name <> 'public.integration_events_id_seq' THEN
        EXECUTE format('ALTER SEQUENCE %s RENAME TO %I', seq_name, 'integration_events_id_seq');
    END IF;
END
$do$;

DO $do$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
               WHERE n.nspname = 'public' AND p.proname = 'jelly_agent_set_updated_at') THEN
        ALTER FUNCTION public.jelly_agent_set_updated_at() RENAME TO jelly_integration_set_updated_at;
    END IF;
END
$do$;

DROP TRIGGER IF EXISTS agents_set_updated_at ON integrations;
DROP TRIGGER IF EXISTS integrations_set_updated_at ON integrations;
CREATE TRIGGER integrations_set_updated_at
BEFORE UPDATE ON integrations
FOR EACH ROW EXECUTE FUNCTION jelly_integration_set_updated_at();

ALTER TABLE integration_events DROP CONSTRAINT IF EXISTS agent_events_actor_type_check;
ALTER TABLE integration_events DROP CONSTRAINT IF EXISTS integration_events_actor_type_check;
ALTER TABLE integration_events ALTER COLUMN actor_type TYPE VARCHAR(16);
UPDATE integration_events SET actor_type = 'integration' WHERE actor_type = 'agent';
ALTER TABLE integration_events
    ADD CONSTRAINT integration_events_actor_type_check CHECK (actor_type IN ('user', 'integration'));

UPDATE integration_events
SET actor_type = CASE WHEN actor_type = 'agent' THEN 'integration' ELSE actor_type END,
    module = CASE WHEN module = 'agents' THEN 'integrations' ELSE module END,
    action = CASE WHEN action LIKE 'agent.%' THEN 'integration.' || substr(action, length('agent.') + 1) ELSE action END,
    route = CASE
        WHEN route LIKE '/api/admin/agents%' THEN '/api/admin/integrations' || substr(route, length('/api/admin/agents') + 1)
        WHEN route LIKE '/api/agent/v1%' THEN '/api/v1' || substr(route, length('/api/agent/v1') + 1)
        ELSE route
    END;

UPDATE system_logs
SET fields = (fields - 'agent_id') || jsonb_build_object('integration_id', fields->'agent_id')
WHERE fields ? 'agent_id';

UPDATE system_logs
SET fields = jsonb_set(fields, '{module}', to_jsonb('integrations'::text), false)
WHERE fields->>'module' = 'agents';

UPDATE system_logs
SET fields = jsonb_set(fields, '{route}', to_jsonb(
    CASE
        WHEN fields->>'route' LIKE '/api/admin/agents%' THEN '/api/admin/integrations' || substr(fields->>'route', length('/api/admin/agents') + 1)
        WHEN fields->>'route' LIKE '/api/agent/v1%' THEN '/api/v1' || substr(fields->>'route', length('/api/agent/v1') + 1)
    END
), false)
WHERE fields->>'route' LIKE '/api/admin/agents%' OR fields->>'route' LIKE '/api/agent/v1%';

UPDATE system_logs
SET message = 'integration.' || substr(message, length('agent.') + 1)
WHERE source = 'audit' AND message LIKE 'agent.%';

DROP TABLE IF EXISTS api_tokens;
