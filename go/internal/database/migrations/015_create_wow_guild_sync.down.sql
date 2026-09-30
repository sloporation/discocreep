DROP TABLE IF EXISTS wow_sync_rank_roles;
DROP TABLE IF EXISTS wow_sync_guilds;
DROP TABLE IF EXISTS wow_sync_settings;

DELETE FROM wow_primary_characters WHERE flavour <> 'retail';
ALTER TABLE wow_primary_characters
    DROP PRIMARY KEY,
    DROP INDEX idx_character,
    DROP COLUMN flavour,
    ADD PRIMARY KEY (guild_id, discord_user_id),
    ADD INDEX idx_character (region, character_id);

DELETE FROM wow_characters WHERE flavour <> 'retail';
ALTER TABLE wow_characters
    DROP PRIMARY KEY,
    DROP COLUMN flavour,
    ADD PRIMARY KEY (region, character_id);
