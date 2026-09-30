// Package shared holds WoW guild sync's settings storage and the sync
// itself. The worker runs the sync (see ../wowSync.go); the web API reads
// and writes the settings through the functions here, so both use the same
// code. It must not import any other wowSync package.
package shared

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/go-sql-driver/mysql"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// Feature names this feature in admin alerts.
const Feature = "wowSync"

// NicknameMode is which character name members' nicknames show.
type NicknameMode string

const (
	NicknameOff      NicknameMode = "off"
	NicknameCombined NicknameMode = "combined" // "Retail / Classic" when both are set
	// Any flavour name (retail, classic, classic_era) shows that flavour's main.
)

// Valid reports whether m is off, combined or a flavour.
func (m NicknameMode) Valid() bool {
	return m == NicknameOff || m == NicknameCombined || blizzard.Flavour(m).Valid()
}

// Settings are a Discord server's guild sync settings.
type Settings struct {
	Enabled      bool         `json:"enabled"`
	NicknameMode NicknameMode `json:"nickname_mode"`
	// RemoveRoles: take away mapped roles a member no longer qualifies for.
	// Off means the sync only ever adds roles.
	RemoveRoles bool `json:"remove_roles"`
}

// DefaultSettings are used until a server saves its own.
var DefaultSettings = Settings{Enabled: false, NicknameMode: NicknameOff, RemoveRoles: true}

// GuildLink is a WoW guild linked to a server. A server can link several,
// including several of one flavour (communities split across guilds by the
// 1,000-member cap).
type GuildLink struct {
	ID           int64            `json:"id"`
	Flavour      blizzard.Flavour `json:"flavour"`
	Region       string           `json:"region"`
	RealmSlug    string           `json:"realm_slug"`
	RealmName    string           `json:"realm"`
	WowGuildSlug string           `json:"guild_slug"`
	WowGuildName string           `json:"guild"`
}

// Status is the outcome of recent syncs.
type Status struct {
	RequestedAt  *time.Time `json:"requested_at,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	LastResult   *Result    `json:"last_result,omitempty"`
	// LastError is set when the last run couldn't sync at all.
	LastError *string `json:"last_error,omitempty"`
}

// Result summarises one sync run.
type Result struct {
	// Linked is how many members have picked at least one main here.
	Linked int `json:"linked"`
	// InGuild is how many of those have a main in at least one linked guild.
	InGuild int `json:"in_guild"`
	// Updated is how many members had their roles or nickname changed.
	Updated int `json:"updated"`
	// GuildErrors are linked guilds whose roster couldn't be read, by link
	// ID. Roles only those guilds grant weren't removed.
	GuildErrors map[int64]string `json:"guild_errors,omitempty"`
	// Errors are per-member failures (e.g. missing permissions), capped.
	Errors []string `json:"errors,omitempty"`
}

// RankKey is a rank in one linked guild.
type RankKey struct {
	LinkID int64
	Rank   int
}

// String is the key's JSON form, e.g. "12:1" (link 12, rank 1).
func (k RankKey) String() string { return strconv.FormatInt(k.LinkID, 10) + ":" + strconv.Itoa(k.Rank) }

// ErrDuplicateGuild: that WoW guild is already linked to the server.
var ErrDuplicateGuild = errors.New("wow guild already linked")

// ErrNoSuchLink: no linked guild with that ID in the server.
var ErrNoSuchLink = errors.New("no such linked guild")

// Load returns the server's settings, linked guilds and status.
func Load(ctx context.Context, db *database.DB, guildID snowflake.ID) (Settings, []GuildLink, Status, error) {
	s, st := DefaultSettings, Status{}
	var mode string
	var result, lastErr sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT enabled, nickname_mode, remove_roles, requested_at, last_synced_at, last_result, last_error
		FROM wow_sync_settings WHERE guild_id = ?`,
		guildID,
	).Scan(&s.Enabled, &mode, &s.RemoveRoles, &st.RequestedAt, &st.LastSyncedAt, &result, &lastErr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, nil, st, err
	}
	if err == nil {
		s.NicknameMode = NicknameMode(mode)
		if result.Valid {
			var r Result
			if json.Unmarshal([]byte(result.String), &r) == nil {
				st.LastResult = &r
			}
		}
		if lastErr.Valid {
			st.LastError = &lastErr.String
		}
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, flavour, region, realm_slug, realm_name, wow_guild_slug, wow_guild_name
		FROM wow_sync_guilds WHERE guild_id = ?
		ORDER BY FIELD(flavour, 'retail', 'classic', 'classic_era'), wow_guild_name, id`,
		guildID,
	)
	if err != nil {
		return s, nil, st, err
	}
	defer rows.Close()
	links := []GuildLink{}
	for rows.Next() {
		var l GuildLink
		if err := rows.Scan(&l.ID, &l.Flavour, &l.Region, &l.RealmSlug, &l.RealmName, &l.WowGuildSlug, &l.WowGuildName); err != nil {
			return s, nil, st, err
		}
		links = append(links, l)
	}
	return s, links, st, rows.Err()
}

// SaveSettings stores the server's settings and requests a sync.
func SaveSettings(ctx context.Context, db *database.DB, guildID snowflake.ID, s Settings) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO wow_sync_settings (guild_id, enabled, nickname_mode, remove_roles, requested_at)
		VALUES (?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), nickname_mode = VALUES(nickname_mode),
		remove_roles = VALUES(remove_roles), requested_at = NOW()`,
		guildID, s.Enabled, s.NicknameMode, s.RemoveRoles,
	)
	return err
}

// AddGuild links a WoW guild to the server and requests a sync. It returns
// ErrDuplicateGuild if that guild is already linked.
func AddGuild(ctx context.Context, db *database.DB, guildID snowflake.ID, l GuildLink) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO wow_sync_guilds (guild_id, flavour, region, realm_slug, realm_name, wow_guild_slug, wow_guild_name)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		guildID, l.Flavour, l.Region, l.RealmSlug, l.RealmName, l.WowGuildSlug, l.WowGuildName,
	)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 { // duplicate key
		return 0, ErrDuplicateGuild
	}
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := ensureSettings(ctx, tx, guildID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE wow_sync_settings SET requested_at = NOW(), last_error = NULL WHERE guild_id = ?", guildID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// RemoveGuild unlinks a WoW guild, with its rank names and rank → role
// mappings. It returns ErrNoSuchLink if the server has no such link.
func RemoveGuild(ctx context.Context, db *database.DB, guildID snowflake.ID, linkID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "DELETE FROM wow_sync_guilds WHERE id = ? AND guild_id = ?", linkID, guildID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchLink
	}
	for _, q := range []string{
		"DELETE FROM wow_sync_rank_roles WHERE link_id = ? AND guild_id = ?",
		"DELETE FROM wow_sync_rank_names WHERE link_id = ? AND guild_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, linkID, guildID); err != nil {
			return err
		}
	}
	if err := requestSync(ctx, tx, guildID); err != nil {
		return err
	}
	return tx.Commit()
}

// RankRoles returns the server's rank → Discord role mapping, for every linked guild.
func RankRoles(ctx context.Context, db *database.DB, guildID snowflake.ID) (map[RankKey]snowflake.ID, error) {
	rows, err := db.QueryContext(ctx, "SELECT link_id, wow_rank, role_id FROM wow_sync_rank_roles WHERE guild_id = ?", guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[RankKey]snowflake.ID{}
	for rows.Next() {
		var k RankKey
		var role snowflake.ID
		if err := rows.Scan(&k.LinkID, &k.Rank, &role); err != nil {
			return nil, err
		}
		m[k] = role
	}
	return m, rows.Err()
}

// RankSetting is an admin's settings for one rank: its in-game name (empty
// = none entered) and the Discord role it gets (0 = none).
type RankSetting struct {
	Name   string
	RoleID snowflake.ID
}

// MaxRankNameLength is the longest rank name stored.
const MaxRankNameLength = 64

// RankNames returns the admin-entered rank names, for every linked guild.
func RankNames(ctx context.Context, db *database.DB, guildID snowflake.ID) (map[RankKey]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT link_id, wow_rank, name FROM wow_sync_rank_names WHERE guild_id = ?", guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[RankKey]string{}
	for rows.Next() {
		var k RankKey
		var name string
		if err := rows.Scan(&k.LinkID, &k.Rank, &name); err != nil {
			return nil, err
		}
		m[k] = name
	}
	return m, rows.Err()
}

// SaveGuildRanks replaces one linked guild's rank names and rank → role
// mappings (other guilds are untouched) and requests a sync. It returns
// ErrNoSuchLink if the server has no such link.
func SaveGuildRanks(ctx context.Context, db *database.DB, guildID snowflake.ID, linkID int64, ranks map[int]RankSetting) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wow_sync_guilds WHERE id = ? AND guild_id = ?", linkID, guildID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNoSuchLink
	}
	for _, q := range []string{
		"DELETE FROM wow_sync_rank_roles WHERE link_id = ?",
		"DELETE FROM wow_sync_rank_names WHERE link_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, linkID); err != nil {
			return err
		}
	}
	for rank, rs := range ranks {
		if rs.RoleID != 0 {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO wow_sync_rank_roles (link_id, guild_id, wow_rank, role_id) VALUES (?, ?, ?, ?)",
				linkID, guildID, rank, rs.RoleID,
			); err != nil {
				return err
			}
		}
		if name := strings.TrimSpace(rs.Name); name != "" {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO wow_sync_rank_names (link_id, guild_id, wow_rank, name) VALUES (?, ?, ?, ?)",
				linkID, guildID, rank, name,
			); err != nil {
				return err
			}
		}
	}
	if err := requestSync(ctx, tx, guildID); err != nil {
		return err
	}
	return tx.Commit()
}

// RequestSync asks the worker to sync the server soon (no-op if it has no
// sync settings). Called for "Sync now" and when a member changes character.
func RequestSync(ctx context.Context, db *database.DB, guildID snowflake.ID) error {
	return requestSync(ctx, db, guildID)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func requestSync(ctx context.Context, db execer, guildID snowflake.ID) error {
	_, err := db.ExecContext(ctx, "UPDATE wow_sync_settings SET requested_at = NOW() WHERE guild_id = ?", guildID)
	return err
}

// ensureSettings creates the server's settings row with defaults if missing.
func ensureSettings(ctx context.Context, db execer, guildID snowflake.ID) error {
	_, err := db.ExecContext(ctx, `
		INSERT IGNORE INTO wow_sync_settings (guild_id, enabled, nickname_mode, remove_roles)
		VALUES (?, ?, ?, ?)`,
		guildID, DefaultSettings.Enabled, DefaultSettings.NicknameMode, DefaultSettings.RemoveRoles,
	)
	return err
}
