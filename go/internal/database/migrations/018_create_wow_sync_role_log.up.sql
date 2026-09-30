-- Every role change WoW guild sync makes, never deleted. The latest entry
-- per (guild, member, role) says whether the bot is responsible for that
-- role on that member: 'add' (the bot gave it) and 'adopt' (the member
-- already had a role their rank grants) mean yes; 'remove' (the bot took it
-- away) and 'release' (it was taken off outside the bot, or the member left)
-- mean no. The sync only takes back roles it's responsible for once they're
-- no longer mapped to a rank.
CREATE TABLE wow_sync_role_log (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    guild_id BIGINT UNSIGNED NOT NULL,
    discord_user_id BIGINT UNSIGNED NOT NULL,
    role_id BIGINT UNSIGNED NOT NULL,
    action ENUM('add', 'remove', 'adopt', 'release') NOT NULL,
    reason VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_member_role (guild_id, discord_user_id, role_id, id)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
