-- One row per member join, recording the invite used (if it could be
-- determined) and who created it. Rejoins add another row.
CREATE TABLE invite_joins (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    invite_code VARCHAR(32) NULL,
    inviter_id BIGINT UNSIGNED NULL,
    inviter_name VARCHAR(100) NULL,
    joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_guild_user (guild_id, user_id, joined_at)
);
