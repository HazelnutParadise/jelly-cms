-- 0015: Jelly MD source for posts and revisions (ticket 09, G0 storage).
--
-- content keeps the sanitized HTML that the server renders from source.
-- format: html = legacy HTML, md = legacy Markdown in meta.markdown, jmd = Jelly MD.
-- render_version and render_trust record which renderer and policy produced content.
--
-- Idempotent: every statement can run again on a migrated database.
-- GORM AutoMigrate creates these columns from the struct tags first, so the
-- column definitions here use the same types and defaults.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS source text;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS format varchar(8) DEFAULT 'html';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS render_version int DEFAULT 0;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS render_trust varchar(8) DEFAULT 'strict';

ALTER TABLE revisions ADD COLUMN IF NOT EXISTS source text;
ALTER TABLE revisions ADD COLUMN IF NOT EXISTS format varchar(8);

-- Backfill. Rows that already have source keep it, so a second run changes nothing.
UPDATE posts
   SET source = meta->>'markdown',
       format = 'md'
 WHERE source IS NULL
   AND meta->>'markdown' IS NOT NULL;

UPDATE posts SET format = 'html' WHERE format IS NULL;
UPDATE posts SET render_version = 0 WHERE render_version IS NULL;
UPDATE posts SET render_trust = 'strict' WHERE render_trust IS NULL;

UPDATE revisions
   SET source = meta->>'markdown',
       format = 'md'
 WHERE source IS NULL
   AND meta->>'markdown' IS NOT NULL;

UPDATE revisions SET format = 'html' WHERE format IS NULL;
