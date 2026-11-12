-- Ticket 29 PR B (ticket 29): plugin
-- notifiers queue notification_deliveries rows with channel 'plugin'
-- (to_address is the plugin id, notification_id is NULL). The channel check
-- from 0005 only allowed email and webhook; 0005 stays untouched and this
-- file replaces the constraint. NOT VALID keeps the table lock short on a
-- large delivery table; VALIDATE then checks existing rows (every existing
-- channel is email or webhook, so it cannot fail). Rerun safe.

ALTER TABLE notification_deliveries
    DROP CONSTRAINT IF EXISTS notification_deliveries_channel_check;

ALTER TABLE notification_deliveries
    ADD CONSTRAINT notification_deliveries_channel_check
    CHECK (channel IN ('email', 'webhook', 'plugin')) NOT VALID;

ALTER TABLE notification_deliveries
    VALIDATE CONSTRAINT notification_deliveries_channel_check;
