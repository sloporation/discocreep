-- Message purge: delete a user's messages from Discord when they leave (if
-- enabled) or on an admin's request. Audit copies in audit_messages are never
-- touched; this only affects Discord.
CREATE TABLE purge_configs (
    guild_id BIGINT UNSIGNED PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- One row per purge. Jobs left queued/running when the bot stops are resumed
-- on the next start.
CREATE TABLE purge_jobs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    `trigger` ENUM('leave', 'command') NOT NULL,
    requested_by BIGINT UNSIGNED NULL,
    status ENUM('queued', 'running', 'done', 'failed') NOT NULL DEFAULT 'queued',
    deleted INT UNSIGNED NOT NULL DEFAULT 0,
    failed INT UNSIGNED NOT NULL DEFAULT 0,
    error TEXT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP NULL,
    finished_at TIMESTAMP NULL,
    INDEX idx_status (status),
    INDEX idx_guild_user (guild_id, user_id, status)
);
