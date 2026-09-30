-- Split message purge's single switch into two independent ones, so a server
-- can allow admin purges without deleting everyone's messages on leave.
-- Guilds that had it enabled keep both behaviours.
ALTER TABLE purge_configs
    RENAME COLUMN enabled TO on_leave,
    ADD COLUMN admin_purge BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE purge_configs SET admin_purge = on_leave;
