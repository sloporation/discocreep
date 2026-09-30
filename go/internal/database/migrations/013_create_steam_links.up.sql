-- Steam accounts linked to Discord users through the web dashboard ("Sign in
-- through Steam"). One Steam account per Discord user, for every guild the
-- bot is in (used to whitelist members for pugs, and later bans).
CREATE TABLE steam_links (
    discord_user_id BIGINT UNSIGNED PRIMARY KEY,
    steam_id BIGINT UNSIGNED NOT NULL, -- SteamID64
    linked_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_steam_id (steam_id)
);

-- Every link and unlink, never deleted, so the Steam accounts a user has
-- ever used stay known (e.g. to ban all of them) after they unlink.
CREATE TABLE steam_link_history (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    discord_user_id BIGINT UNSIGNED NOT NULL,
    steam_id BIGINT UNSIGNED NOT NULL,
    action ENUM('linked', 'unlinked') NOT NULL,
    occurred_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_discord_user (discord_user_id),
    INDEX idx_steam_id (steam_id)
);
