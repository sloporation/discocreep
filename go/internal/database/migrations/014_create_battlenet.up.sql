-- Battle.net accounts linked to Discord users through the web dashboard
-- (OAuth), and the retail WoW characters on them. Blizzard tokens aren't
-- stored: characters are read when the user links or refreshes.
CREATE TABLE battlenet_links (
    discord_user_id BIGINT UNSIGNED PRIMARY KEY,
    battlenet_id BIGINT UNSIGNED NOT NULL,
    battletag VARCHAR(64) NOT NULL,
    linked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    characters_synced_at TIMESTAMP NULL,
    INDEX idx_battlenet_id (battlenet_id)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Every link and unlink, never deleted.
CREATE TABLE battlenet_link_history (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    discord_user_id BIGINT UNSIGNED NOT NULL,
    battlenet_id BIGINT UNSIGNED NOT NULL,
    battletag VARCHAR(64) NOT NULL,
    action ENUM('linked', 'unlinked') NOT NULL,
    occurred_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_discord_user (discord_user_id),
    INDEX idx_battlenet_id (battlenet_id)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Retail characters on a user's linked Battle.net account, as of the last
-- sync. Character IDs are only unique within a region.
CREATE TABLE wow_characters (
    region CHAR(2) NOT NULL,
    character_id BIGINT UNSIGNED NOT NULL,
    discord_user_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    realm_slug VARCHAR(64) NOT NULL,
    realm_name VARCHAR(64) NOT NULL,
    level SMALLINT UNSIGNED NOT NULL,
    class_id SMALLINT UNSIGNED NOT NULL,
    class_name VARCHAR(32) NOT NULL,
    race_name VARCHAR(32) NOT NULL,
    faction VARCHAR(16) NOT NULL, -- ALLIANCE, HORDE, NEUTRAL
    synced_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (region, character_id),
    INDEX idx_discord_user (discord_user_id),
    INDEX idx_realm_name (region, realm_slug, name)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- The character a member represents themselves with in a Discord server.
-- Future guild sync uses it for roles and nicknames.
CREATE TABLE wow_primary_characters (
    guild_id BIGINT UNSIGNED NOT NULL,
    discord_user_id BIGINT UNSIGNED NOT NULL,
    region CHAR(2) NOT NULL,
    character_id BIGINT UNSIGNED NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (guild_id, discord_user_id),
    INDEX idx_character (region, character_id)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci; -- must match wow_characters (joined on region)
