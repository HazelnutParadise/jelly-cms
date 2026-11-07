-- Ticket 29 PR B (docs/tickets/29-plugin-read-api-and-extensions.md): plugin
-- notifiers queue notification_deliveries rows with channel 'plugin'
-- (to_address is the plugin id, notification_id is NULL). The channel check
-- from 0005 only allowed email and webhook; 0005 stays untouched and this
-- file replaces the constraint. Rerun safe.

ALTER TABLE notification_deliveries
    DROP CONSTRAINT IF EXISTS notification_deliveries_channel_check;

ALTER TABLE notification_deliveries
    ADD CONSTRAINT notification_deliveries_channel_check
    CHECK (channel IN ('email', 'webhook', 'plugin'));
