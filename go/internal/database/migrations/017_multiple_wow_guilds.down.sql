-- Back to one linked guild per flavour: keeps the first-linked guild of each
-- flavour (and its ranks) and drops the rest.

DELETE g FROM wow_sync_guilds g
    JOIN wow_sync_guilds keep ON keep.guild_id = g.guild_id AND keep.flavour = g.flavour AND keep.id < g.id;

ALTER TABLE wow_sync_rank_roles ADD COLUMN flavour VARCHAR(16) NULL AFTER guild_id;
UPDATE wow_sync_rank_roles r JOIN wow_sync_guilds g ON g.id = r.link_id SET r.flavour = g.flavour;
DELETE FROM wow_sync_rank_roles WHERE flavour IS NULL;
ALTER TABLE wow_sync_rank_roles
    DROP PRIMARY KEY,
    DROP INDEX idx_guild,
    DROP COLUMN link_id,
    MODIFY flavour VARCHAR(16) NOT NULL,
    ADD PRIMARY KEY (guild_id, flavour, wow_rank);

ALTER TABLE wow_sync_rank_names ADD COLUMN flavour VARCHAR(16) NULL AFTER guild_id;
UPDATE wow_sync_rank_names n JOIN wow_sync_guilds g ON g.id = n.link_id SET n.flavour = g.flavour;
DELETE FROM wow_sync_rank_names WHERE flavour IS NULL;
ALTER TABLE wow_sync_rank_names
    DROP PRIMARY KEY,
    DROP INDEX idx_guild,
    DROP COLUMN link_id,
    MODIFY flavour VARCHAR(16) NOT NULL,
    ADD PRIMARY KEY (guild_id, flavour, wow_rank);

ALTER TABLE wow_sync_guilds
    DROP PRIMARY KEY,
    DROP INDEX uq_wow_guild,
    DROP INDEX idx_guild,
    DROP COLUMN id,
    ADD PRIMARY KEY (guild_id, flavour);
