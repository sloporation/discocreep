-- Admin-entered names for WoW guild ranks. Blizzard's API only exposes rank
-- numbers (0 = Guild Master), so the dashboard asks for the in-game names.
CREATE TABLE wow_sync_rank_names (
    guild_id BIGINT UNSIGNED NOT NULL,
    flavour VARCHAR(16) NOT NULL,
    wow_rank TINYINT UNSIGNED NOT NULL,
    name VARCHAR(64) NOT NULL,
    PRIMARY KEY (guild_id, flavour, wow_rank)
) DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
