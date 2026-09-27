-- Guild auditing. utf8mb4 throughout: usernames, messages and reaction
-- emoji routinely contain 4-byte characters.

-- Every user who has ever joined each guild, with their current state.
CREATE TABLE audit_members (
    guild_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    username VARCHAR(100) NOT NULL,
    first_joined_at TIMESTAMP NULL,
    last_joined_at TIMESTAMP NULL,
    last_left_at TIMESTAMP NULL,
    in_guild BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (guild_id, user_id)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Every join and leave. source says how it was observed:
--   gateway      - live event while the bot was online
--   bot_offline  - found when reconciling the member list after the bot was
--                  offline (leaves are timed at when the bot was last seen)
--   initial_sync - existing member recorded the first time the guild was synced
CREATE TABLE audit_member_events (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    event ENUM('join', 'leave') NOT NULL,
    source ENUM('gateway', 'bot_offline', 'initial_sync') NOT NULL,
    occurred_at TIMESTAMP NOT NULL,
    recorded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_guild_user (guild_id, user_id, occurred_at)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Every message posted in a guild channel.
CREATE TABLE audit_messages (
    message_id BIGINT UNSIGNED PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    channel_id BIGINT UNSIGNED NOT NULL,
    author_id BIGINT UNSIGNED NOT NULL,
    author_bot BOOLEAN NOT NULL DEFAULT FALSE,
    content TEXT NOT NULL,
    attachments TEXT NULL, -- JSON array of attachment URLs, NULL if none
    created_at TIMESTAMP NOT NULL,
    INDEX idx_guild_channel (guild_id, channel_id, created_at),
    INDEX idx_guild_author (guild_id, author_id, created_at)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Every reaction added or removed. emoji is the unicode emoji, or the custom
-- emoji's name; emoji_id is set for custom emoji.
CREATE TABLE audit_reactions (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    channel_id BIGINT UNSIGNED NOT NULL,
    message_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    emoji VARCHAR(100) NOT NULL,
    emoji_id BIGINT UNSIGNED NULL,
    action ENUM('add', 'remove') NOT NULL,
    occurred_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_message (message_id),
    INDEX idx_guild_user (guild_id, user_id, occurred_at)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Per-guild bookkeeping. last_seen_at is refreshed every minute while the bot
-- is connected, so after downtime it marks roughly when the bot went offline.
CREATE TABLE audit_guild_state (
    guild_id BIGINT UNSIGNED PRIMARY KEY,
    last_seen_at TIMESTAMP NOT NULL,
    last_synced_at TIMESTAMP NULL
);
