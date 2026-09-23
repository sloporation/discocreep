CREATE TABLE channels (
    channel_id BIGINT UNSIGNED PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    parent_id BIGINT UNSIGNED NULL,
    channel_type TINYINT UNSIGNED NOT NULL,
    name VARCHAR(100) NOT NULL,
    managed BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_guild_id (guild_id),
    INDEX idx_parent_id (parent_id),
    INDEX idx_managed (managed),
    INDEX idx_deleted (deleted_at),
    FOREIGN KEY (guild_id) REFERENCES guilds(id) ON DELETE CASCADE
);
