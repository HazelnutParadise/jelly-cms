-- Ticket 07: extend notification_deliveries for guest/shop emails
-- Additive, rerun safe. Do not modify 0005/0008.

ALTER TABLE notification_deliveries
    ADD COLUMN IF NOT EXISTS kind VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS to_address TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS params JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE notification_deliveries
    ALTER COLUMN notification_id DROP NOT NULL;

-- Backfill snapshots for existing deliveries where possible
DO $$
BEGIN
    -- Fill from notifications
    UPDATE notification_deliveries d
    SET kind = COALESCE(NULLIF(d.kind,''), n.kind),
        params = CASE
            WHEN d.params IS NULL OR d.params = '{}'::jsonb THEN COALESCE(n.params, '{}'::jsonb)
            ELSE d.params
        END
    FROM notifications n
    WHERE d.notification_id = n.id
      AND (d.kind = '' OR d.params IS NULL OR d.params = '{}'::jsonb);

    -- Fill to_address from user if missing (email channel)
    UPDATE notification_deliveries d
    SET to_address = COALESCE(NULLIF(d.to_address,''), u.email)
    FROM notifications n
    JOIN users u ON u.id = n.user_id
    WHERE d.notification_id = n.id
      AND d.channel = 'email'
      AND (d.to_address IS NULL OR d.to_address = '');
END
$$;
