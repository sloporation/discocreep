ALTER TABLE guilds
ADD COLUMN join_channel_id BIGINT UNSIGNED,
ADD COLUMN join_admin_channel_id BIGINT UNSIGNED,
ADD COLUMN leave_channel_id BIGINT UNSIGNED,
ADD COLUMN leave_admin_channel_id BIGINT UNSIGNED;
