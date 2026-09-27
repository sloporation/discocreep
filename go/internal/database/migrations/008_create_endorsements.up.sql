-- Per-guild community endorsement settings. New joiners get a sponsorship
-- post in channel_id; sponsoring grants member_role_id.
CREATE TABLE endorsement_configs (
    guild_id BIGINT UNSIGNED PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    channel_id BIGINT UNSIGNED NOT NULL,
    member_role_id BIGINT UNSIGNED NOT NULL,
    forum_tag_id BIGINT UNSIGNED NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- One row per join while the feature is enabled. channel_id/message_id point
-- at the sponsorship post (a text channel message, or a forum thread's
-- starter message, where channel_id is the thread).
CREATE TABLE endorsements (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    channel_id BIGINT UNSIGNED NULL,
    message_id BIGINT UNSIGNED NULL,
    status ENUM('pending', 'sponsored', 'left') NOT NULL DEFAULT 'pending',
    sponsor_id BIGINT UNSIGNED NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    sponsored_at TIMESTAMP NULL,
    INDEX idx_guild_user (guild_id, user_id, status)
);
