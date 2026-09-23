CREATE TABLE ticket_configs (
    guild_id BIGINT UNSIGNED PRIMARY KEY,
    support_channel_id BIGINT UNSIGNED NOT NULL,
    archive_category_id BIGINT UNSIGNED NOT NULL,
    notify_role_id BIGINT UNSIGNED NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tickets (
    channel_id BIGINT UNSIGNED PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    creator_id BIGINT UNSIGNED NOT NULL,
    category VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    status ENUM('open', 'closed') DEFAULT 'open',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    closed_at TIMESTAMP NULL
);
