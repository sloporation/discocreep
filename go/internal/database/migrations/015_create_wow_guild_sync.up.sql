-- WoW Classic support, and WoW guild sync.

-- Characters and primary-character choices gain a flavour (game version):
-- retail, classic (Progression) or classic_era. Existing rows are retail.
-- Character IDs are only unique within a flavour and region.
ALTER TABLE wow_characters
    ADD COLUMN flavour VARCHAR(16) NOT NULL DEFAULT 'retail' FIRST,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (flavour, region, character_id);

-- A member picks one main per flavour, per Discord server.
ALTER TABLE wow_primary_characters
    ADD COLUMN flavour VARCHAR(16) NOT NULL DEFAULT 'retail' AFTER discord_user_id,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (guild_id, discord_user_id, flavour),
    DROP INDEX idx_character,
    ADD INDEX idx_character (flavour, region, character_id);

-- Guild sync settings for a Discord server.
CREATE TABLE wow_sync_settings (
    guild_id BIGINT UNSIGNED PRIMARY KEY, -- Discord server
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    -- Which character name members' nicknames show: off, retail, classic,
    -- classic_era, or combined ("Retail / Classic" when both are available).
    nickname_mode VARCHAR(16) NOT NULL DEFAULT 'off',
    -- Remove mapped roles a member is no longer entitled to (off = only add).
    remove_roles BOOLEAN NOT NULL DEFAULT TRUE,
    -- Set when a sync should run soon (settings saved, "Sync now", a member
    -- changed character); the worker runs it on its next check.
    requested_at TIMESTAMP NULL,
    last_synced_at TIMESTAMP NULL,
    last_result TEXT NULL, -- JSON summary of the last run
    last_error TEXT NULL,  -- guild-level problems (e.g. a roster couldn't be read), or NULL
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- The WoW guild linked to a Discord server, at most one per flavour.
CREATE TABLE wow_sync_guilds (
    guild_id BIGINT UNSIGNED NOT NULL,
    flavour VARCHAR(16) NOT NULL,
    region CHAR(2) NOT NULL,
    realm_slug VARCHAR(64) NOT NULL,
    realm_name VARCHAR(64) NOT NULL,
    wow_guild_slug VARCHAR(128) NOT NULL,
    wow_guild_name VARCHAR(128) NOT NULL,
    PRIMARY KEY (guild_id, flavour)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- Which Discord role each WoW guild rank gets. Several ranks (in the same
-- or different flavours) may share a role. Only roles listed here are ever
-- added or removed by the sync.
CREATE TABLE wow_sync_rank_roles (
    guild_id BIGINT UNSIGNED NOT NULL,
    flavour VARCHAR(16) NOT NULL,
    wow_rank TINYINT UNSIGNED NOT NULL, -- 0 = Guild Master
    role_id BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (guild_id, flavour, wow_rank)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
