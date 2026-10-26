-- Controls visibility in the in-app notification center.
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS in_app_visible BOOLEAN NOT NULL DEFAULT TRUE;
