-- Allow several linked WoW guilds per flavour (large communities split
-- across guilds because of the 1,000-member cap). Each linked guild gets its
-- own ID, and rank names/roles are keyed by it instead of by flavour.

ALTER TABLE wow_sync_guilds
    DROP PRIMARY KEY,
    ADD COLUMN id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY FIRST,
    ADD UNIQUE KEY uq_wow_guild (guild_id, flavour, region, realm_slug, wow_guild_slug),
    ADD INDEX idx_guild (guild_id);

-- Rank → role: point existing rows at their guild's new ID.
ALTER TABLE wow_sync_rank_roles ADD COLUMN link_id BIGINT UNSIGNED NULL FIRST;
UPDATE wow_sync_rank_roles r
    JOIN wow_sync_guilds g ON g.guild_id = r.guild_id AND g.flavour = r.flavour
    SET r.link_id = g.id;
DELETE FROM wow_sync_rank_roles WHERE link_id IS NULL;
ALTER TABLE wow_sync_rank_roles
    DROP PRIMARY KEY,
    DROP COLUMN flavour,
    MODIFY link_id BIGINT UNSIGNED NOT NULL,
    ADD PRIMARY KEY (link_id, wow_rank),
    ADD INDEX idx_guild (guild_id);

-- Rank names: the same.
ALTER TABLE wow_sync_rank_names ADD COLUMN link_id BIGINT UNSIGNED NULL FIRST;
UPDATE wow_sync_rank_names n
    JOIN wow_sync_guilds g ON g.guild_id = n.guild_id AND g.flavour = n.flavour
    SET n.link_id = g.id;
DELETE FROM wow_sync_rank_names WHERE link_id IS NULL;
ALTER TABLE wow_sync_rank_names
    DROP PRIMARY KEY,
    DROP COLUMN flavour,
    MODIFY link_id BIGINT UNSIGNED NOT NULL,
    ADD PRIMARY KEY (link_id, wow_rank),
    ADD INDEX idx_guild (guild_id);
