CREATE TABLE IF NOT EXISTS widgets (
 id bigserial PRIMARY KEY, name varchar(80) NOT NULL UNIQUE, title varchar(160) NOT NULL DEFAULT '',
 version integer NOT NULL DEFAULT 0, status varchar(16) NOT NULL DEFAULT 'draft',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
ALTER TABLE widgets ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
CREATE INDEX IF NOT EXISTS idx_widgets_deleted_at ON widgets(deleted_at);

CREATE TABLE IF NOT EXISTS widget_versions (
 id bigserial PRIMARY KEY, widget_id bigint NOT NULL REFERENCES widgets(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
 version integer NOT NULL, source text NOT NULL DEFAULT '', params jsonb NOT NULL DEFAULT '[]'::jsonb,
 published boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz, UNIQUE(widget_id,version)
);
ALTER TABLE widget_versions ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE widget_versions ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
CREATE INDEX IF NOT EXISTS idx_widget_versions_published ON widget_versions(widget_id,published);
CREATE INDEX IF NOT EXISTS idx_widget_versions_created_at ON widget_versions(created_at);
CREATE INDEX IF NOT EXISTS idx_widget_versions_deleted_at ON widget_versions(deleted_at);
ALTER TABLE widget_versions DROP CONSTRAINT IF EXISTS widget_versions_widget_id_fkey;
ALTER TABLE widget_versions ADD CONSTRAINT widget_versions_widget_id_fkey FOREIGN KEY (widget_id) REFERENCES widgets(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

CREATE OR REPLACE FUNCTION jelly_widget_version_set_updated_at()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN NEW.updated_at := clock_timestamp(); RETURN NEW; END; $$;
DROP TRIGGER IF EXISTS widget_versions_set_updated_at ON widget_versions;
CREATE TRIGGER widget_versions_set_updated_at BEFORE UPDATE ON widget_versions FOR EACH ROW EXECUTE FUNCTION jelly_widget_version_set_updated_at();

CREATE OR REPLACE FUNCTION jelly_widget_version_immutable()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
 IF TG_OP = 'DELETE' AND OLD.published THEN
  RAISE EXCEPTION 'published widget versions are immutable';
 END IF;
 IF TG_OP = 'UPDATE' AND OLD.published AND (
  NEW.widget_id IS DISTINCT FROM OLD.widget_id OR NEW.version IS DISTINCT FROM OLD.version OR
  NEW.source IS DISTINCT FROM OLD.source OR NEW.params IS DISTINCT FROM OLD.params OR
  NEW.published IS DISTINCT FROM OLD.published OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at
 ) THEN
  RAISE EXCEPTION 'published widget versions are immutable';
 END IF;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS widget_versions_immutable ON widget_versions;
CREATE TRIGGER widget_versions_immutable BEFORE UPDATE OR DELETE ON widget_versions FOR EACH ROW EXECUTE FUNCTION jelly_widget_version_immutable();

CREATE TABLE IF NOT EXISTS widget_audit_log (
 id bigserial PRIMARY KEY, widget_id bigint NOT NULL, name varchar(80) NOT NULL, action text NOT NULL,
 old_state jsonb, new_state jsonb, actor_id bigint, actor_email text, actor_name text,
 created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT widget_audit_log_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE SET NULL ON UPDATE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_widget_audit_log_created_at ON widget_audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_widget_audit_log_actor_id ON widget_audit_log(actor_id);

CREATE TABLE IF NOT EXISTS widget_version_audit_log (
 id bigserial PRIMARY KEY, widget_version_id bigint NOT NULL, widget_id bigint NOT NULL,
 version integer NOT NULL, action text NOT NULL, old_state jsonb, new_state jsonb,
 actor_id bigint, actor_email text, actor_name text, created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT widget_version_audit_log_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES users(id) ON DELETE SET NULL ON UPDATE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_widget_version_audit_log_created_at ON widget_version_audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_widget_version_audit_log_widget_id ON widget_version_audit_log(widget_id);

CREATE OR REPLACE FUNCTION jelly_widget_params_summary(p JSONB)
RETURNS JSONB LANGUAGE sql IMMUTABLE SET search_path = pg_catalog, public AS $$
 SELECT COALESCE(jsonb_agg(item - 'default'), '[]'::jsonb)
 FROM jsonb_array_elements(COALESCE(p, '[]'::jsonb)) AS params(item);
$$;

CREATE OR REPLACE FUNCTION jelly_widget_version_audit_change()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE v_actor_id BIGINT; v_actor_email TEXT; v_actor_name TEXT; v_old JSONB; v_new JSONB;
BEGIN
 v_actor_id := NULLIF(current_setting('jelly.actor_id', true), '')::BIGINT;
 IF v_actor_id IS NOT NULL THEN SELECT email, display_name INTO v_actor_email, v_actor_name FROM public.users WHERE id=v_actor_id; END IF;
 IF TG_OP IN ('UPDATE','DELETE') THEN v_old := jsonb_build_object('id',OLD.id,'widget_id',OLD.widget_id,'version',OLD.version,'published',OLD.published,'params',public.jelly_widget_params_summary(OLD.params),'deleted_at',OLD.deleted_at); END IF;
 IF TG_OP IN ('INSERT','UPDATE') THEN v_new := jsonb_build_object('id',NEW.id,'widget_id',NEW.widget_id,'version',NEW.version,'published',NEW.published,'params',public.jelly_widget_params_summary(NEW.params),'deleted_at',NEW.deleted_at); END IF;
 INSERT INTO public.widget_version_audit_log(widget_version_id,widget_id,version,action,old_state,new_state,actor_id,actor_email,actor_name)
 VALUES(CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END, CASE WHEN TG_OP='DELETE' THEN OLD.widget_id ELSE NEW.widget_id END, CASE WHEN TG_OP='DELETE' THEN OLD.version ELSE NEW.version END, TG_OP,v_old,v_new,v_actor_id,v_actor_email,v_actor_name);
 DELETE FROM public.widget_version_audit_log WHERE created_at < now() - interval '90 days';
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END; $$;
DROP TRIGGER IF EXISTS widget_versions_audit_change ON widget_versions;
CREATE TRIGGER widget_versions_audit_change AFTER INSERT OR UPDATE OR DELETE ON widget_versions FOR EACH ROW EXECUTE FUNCTION jelly_widget_version_audit_change();

CREATE OR REPLACE FUNCTION jelly_widget_set_updated_at()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN NEW.updated_at := clock_timestamp(); RETURN NEW; END; $$;
DROP TRIGGER IF EXISTS widgets_set_updated_at ON widgets;
CREATE TRIGGER widgets_set_updated_at BEFORE UPDATE ON widgets FOR EACH ROW EXECUTE FUNCTION jelly_widget_set_updated_at();

CREATE OR REPLACE FUNCTION jelly_widget_audit_change()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE v_actor_id BIGINT; v_actor_email TEXT; v_actor_name TEXT; v_old JSONB; v_new JSONB;
BEGIN
 v_actor_id := NULLIF(current_setting('jelly.actor_id', true), '')::BIGINT;
 IF v_actor_id IS NOT NULL THEN SELECT email, display_name INTO v_actor_email, v_actor_name FROM public.users WHERE id=v_actor_id; END IF;
 IF TG_OP IN ('UPDATE','DELETE') THEN v_old := jsonb_build_object('id',OLD.id,'title',OLD.title,'version',OLD.version,'status',OLD.status,'deleted_at',OLD.deleted_at); END IF;
 IF TG_OP IN ('INSERT','UPDATE') THEN v_new := jsonb_build_object('id',NEW.id,'title',NEW.title,'version',NEW.version,'status',NEW.status,'deleted_at',NEW.deleted_at); END IF;
 INSERT INTO public.widget_audit_log(widget_id,name,action,old_state,new_state,actor_id,actor_email,actor_name)
 VALUES(CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END, CASE WHEN TG_OP='DELETE' THEN OLD.name ELSE NEW.name END, TG_OP,v_old,v_new,v_actor_id,v_actor_email,v_actor_name);
 DELETE FROM public.widget_audit_log WHERE created_at < now() - interval '90 days';
 RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END; $$;
DROP TRIGGER IF EXISTS widgets_audit_change ON widgets;
CREATE TRIGGER widgets_audit_change AFTER INSERT OR UPDATE OR DELETE ON widgets FOR EACH ROW EXECUTE FUNCTION jelly_widget_audit_change();

ALTER TABLE widgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE widget_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE widget_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE widget_version_audit_log ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON TABLE widgets FROM PUBLIC;
REVOKE ALL ON TABLE widget_versions FROM PUBLIC;
REVOKE ALL ON TABLE widget_audit_log FROM PUBLIC;
REVOKE ALL ON TABLE widget_version_audit_log FROM PUBLIC;
