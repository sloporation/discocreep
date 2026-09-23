ALTER TABLE ticket_configs
    ADD COLUMN channel_name_format VARCHAR(100) NOT NULL DEFAULT 'ticket-{username}';
