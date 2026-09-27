-- Server-wide channel where features post alerts that need an admin's
-- attention (e.g. a post that couldn't be made). NULL = not configured.
ALTER TABLE guilds
ADD COLUMN admin_alert_channel_id BIGINT UNSIGNED NULL;
