// Package alerts posts messages to a guild's configured admin alerts channel.
//
// Features use it when something fails that only an admin can fix (a missing
// permission, a deleted channel or tag, ...), so the failure is visible in
// Discord and not just in the bot's logs. The channel is set per guild with
// /adminalerts (see internal/discord/adminAlerts).
//
// An *Alerter is created once in discord.New and exposed as bot.Alerts; pass
// it into handler constructors the same way as bot.DB. This package must not
// import internal/discord or any feature package, so every feature's
// subpackages can use it.
package alerts

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

const colorAlert = 0xED4245

// Alert is one message for admins.
type Alert struct {
	// Feature is the feature raising the alert, shown in the footer (e.g. "communityEndorsement").
	Feature string
	// Title is a short summary of what went wrong.
	Title string
	// Description says what happened and what the admin should do about it.
	Description string
	// Fields are optional extra details (error text, IDs, ...).
	Fields []discord.EmbedField
}

// Alerter looks up and posts to each guild's admin alerts channel.
type Alerter struct {
	db *database.DB
}

// New returns an Alerter backed by db.
func New(db *database.DB) *Alerter {
	return &Alerter{db: db}
}

// Channel returns the guild's admin alerts channel, or nil if none is set.
func (a *Alerter) Channel(ctx context.Context, guildID snowflake.ID) (*snowflake.ID, error) {
	var channelID *snowflake.ID
	err := a.db.QueryRowContext(ctx,
		"SELECT admin_alert_channel_id FROM guilds WHERE id = ?",
		guildID,
	).Scan(&channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return channelID, err
}

// SetChannel sets (or with nil, clears) the guild's admin alerts channel.
// guildName is only used if the guild has no guilds row yet (name is NOT NULL).
func (a *Alerter) SetChannel(ctx context.Context, guildID snowflake.ID, guildName string, channelID *snowflake.ID) error {
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO guilds (id, name, admin_alert_channel_id) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE admin_alert_channel_id = VALUES(admin_alert_channel_id)`,
		guildID, guildName, channelID,
	)
	return err
}

// Send posts alert to the guild's admin alerts channel. It is best effort:
// if no channel is configured or posting fails, it only logs, so callers can
// fire and forget. The alert is always logged at Warn level too.
func (a *Alerter) Send(client *bot.Client, guildID snowflake.ID, alert Alert) {
	slog.Warn("admin alert", "guild_id", guildID, "feature", alert.Feature, "title", alert.Title, "description", alert.Description)

	channelID, err := a.Channel(context.Background(), guildID)
	if err != nil {
		slog.Error("alerts: look up channel", "guild_id", guildID, "err", err)
		return
	}
	if channelID == nil {
		slog.Debug("alerts: no admin alerts channel configured", "guild_id", guildID)
		return
	}

	now := time.Now()
	if _, err := client.Rest.CreateMessage(*channelID, discord.MessageCreate{
		Embeds: []discord.Embed{{
			Color:       colorAlert,
			Title:       "⚠️ " + alert.Title,
			Description: alert.Description,
			Fields:      alert.Fields,
			Footer:      &discord.EmbedFooter{Text: alert.Feature},
			Timestamp:   &now,
		}},
	}); err != nil {
		slog.Error("alerts: post alert", "guild_id", guildID, "channel_id", *channelID, "err", err)
	}
}

// ErrorField formats an error as an embed field for Alert.Fields.
func ErrorField(err error) discord.EmbedField {
	msg := err.Error()
	if len(msg) > 1000 { // embed field values max out at 1024 chars
		msg = msg[:1000] + "…"
	}
	return discord.EmbedField{Name: "Error", Value: "```" + msg + "```"}
}
