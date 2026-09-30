ALTER TABLE purge_configs
    DROP COLUMN admin_purge,
    RENAME COLUMN on_leave TO enabled;
