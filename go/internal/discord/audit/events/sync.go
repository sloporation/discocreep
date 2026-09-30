// Reconcile the stored member list with Discord's whenever a guild becomes
// available (bot start, reconnect, or being added to a guild), recording any
// joins and leaves that happened while the bot was offline.
package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// memberPageSize is the most members Discord returns per request.
const memberPageSize = 1000

// HandleGuildReady returns a listener that syncs members for each guild
// available at startup (or after a reconnect).
func HandleGuildReady(db *database.DB, alerter *alerts.Alerter) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildReady) {
		syncGuild(e.Client(), db, alerter, e.GuildID)
	})
}

// HandleGuildJoin returns a listener that syncs members when the bot is
// added to a new guild.
func HandleGuildJoin(db *database.DB, alerter *alerts.Alerter) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildJoin) {
		syncGuild(e.Client(), db, alerter, e.GuildID)
	})
}

// currentMember is what the sync needs to know about a member in Discord.
type currentMember struct {
	username string
	joinedAt *time.Time
}

// storedMember is a member the database says is in the guild.
type storedMember struct {
	updatedAt time.Time
}

// diffMembers returns who has joined (in Discord, not stored as in the
// guild) and who has left (stored as in the guild, not in Discord) since the
// last sync. Stored members updated at or after syncStart are never treated
// as left: they joined via the gateway after the member list was fetched.
func diffMembers(current map[snowflake.ID]currentMember, stored map[snowflake.ID]storedMember, syncStart time.Time) (joined, left []snowflake.ID) {
	for id := range current {
		if _, ok := stored[id]; !ok {
			joined = append(joined, id)
		}
	}
	for id, s := range stored {
		if _, ok := current[id]; !ok && s.updatedAt.Before(syncStart) {
			left = append(left, id)
		}
	}
	return joined, left
}

// syncGuild reconciles audit_members with the guild's current member list.
//
// On a guild's first sync every current member is recorded as an
// initial_sync join at their Discord join time. After that, differences are
// recorded as bot_offline events: joins at the member's real join time
// (Discord provides it), leaves at when the bot was last seen online (the
// real time isn't knowable).
func syncGuild(client *bot.Client, db *database.DB, alerter *alerts.Alerter, guildID snowflake.ID) {
	ctx := context.Background()
	started := time.Now()

	// Use the database's clock for the cut-off, so it compares cleanly with
	// updated_at regardless of time zones.
	var syncStart time.Time
	if err := db.QueryRowContext(ctx, "SELECT NOW()").Scan(&syncStart); err != nil {
		slog.Error("audit: sync start time", "guild_id", guildID, "err", err)
		return
	}

	var lastSeen *time.Time
	err := db.QueryRowContext(ctx,
		"SELECT last_seen_at FROM audit_guild_state WHERE guild_id = ?",
		guildID,
	).Scan(&lastSeen)
	firstSync := errors.Is(err, sql.ErrNoRows)
	if err != nil && !firstSync {
		slog.Error("audit: load guild state", "guild_id", guildID, "err", err)
		return
	}

	current, err := fetchMembers(client, guildID)
	if err != nil {
		slog.Error("audit: fetch members", "guild_id", guildID, "err", err)
		alerter.Send(client, guildID, alerts.Alert{
			Feature:     feature,
			Title:       "Couldn't sync the member list",
			Description: "The bot couldn't fetch this server's member list, so joins and leaves while it was offline weren't recorded. It will try again next time it connects.",
			Fields:      []discord.EmbedField{alerts.ErrorField(err)},
		})
		return
	}

	stored, err := loadStoredMembers(ctx, db, guildID)
	if err != nil {
		slog.Error("audit: load stored members", "guild_id", guildID, "err", err)
		return
	}

	joined, left := diffMembers(current, stored, syncStart)

	joinSource := sourceBotOffline
	if firstSync {
		joinSource = sourceInitialSync
	}
	var failed int
	for _, id := range joined {
		m := current[id]
		joinedAt := syncStart
		if m.joinedAt != nil {
			joinedAt = *m.joinedAt
		}
		if err := recordJoin(ctx, db, guildID, id, m.username, joinedAt, joinSource); err != nil {
			slog.Error("audit: record synced join", "guild_id", guildID, "user_id", id, "err", err)
			failed++
		}
	}

	leftAt := syncStart
	if lastSeen != nil {
		leftAt = *lastSeen
	}
	for _, id := range left {
		if err := recordLeave(ctx, db, guildID, id, "", leftAt, sourceBotOffline); err != nil {
			slog.Error("audit: record synced leave", "guild_id", guildID, "user_id", id, "err", err)
			failed++
		}
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO audit_guild_state (guild_id, last_seen_at, last_synced_at) VALUES (?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE last_seen_at = NOW(), last_synced_at = NOW()`,
		guildID,
	); err != nil {
		slog.Error("audit: save guild state", "guild_id", guildID, "err", err)
	}

	slog.Info("audit: member sync complete",
		"guild_id", guildID, "first_sync", firstSync, "members", len(current),
		"joined", len(joined), "left", len(left), "failed", failed, "took", time.Since(started).Round(time.Millisecond))
}

// fetchMembers pages through the guild's full member list over REST.
func fetchMembers(client *bot.Client, guildID snowflake.ID) (map[snowflake.ID]currentMember, error) {
	members := make(map[snowflake.ID]currentMember)
	var after snowflake.ID
	for {
		page, err := client.Rest.GetMembers(guildID, memberPageSize, after)
		if err != nil {
			return nil, fmt.Errorf("list members after %s: %w", after, err)
		}
		for _, m := range page {
			members[m.User.ID] = currentMember{username: m.User.Username, joinedAt: m.JoinedAt}
			after = max(after, m.User.ID)
		}
		if len(page) < memberPageSize {
			return members, nil
		}
	}
}

// loadStoredMembers returns the members the database says are in the guild.
func loadStoredMembers(ctx context.Context, db *database.DB, guildID snowflake.ID) (map[snowflake.ID]storedMember, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT user_id, updated_at FROM audit_members WHERE guild_id = ? AND in_guild",
		guildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stored := make(map[snowflake.ID]storedMember)
	for rows.Next() {
		var id snowflake.ID
		var s storedMember
		if err := rows.Scan(&id, &s.updatedAt); err != nil {
			return nil, err
		}
		stored[id] = s
	}
	return stored, rows.Err()
}
