// Seed the invite cache when each guild becomes available.
package events

import (
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/shared"
)

// HandleGuildReady returns a listener that snapshots each guild's invites
// after connecting, so the first join can be diffed against it.
func HandleGuildReady(alerter *alerts.Alerter, cache *shared.InviteCache) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildReady) {
		invites, err := e.Client().Rest.GetGuildInvites(e.GuildID)
		if err != nil {
			slog.Warn("inviteTracker: fetch invites", "guild_id", e.GuildID, "err", err)
			alerter.Send(e.Client(), e.GuildID, alerts.Alert{
				Feature:     shared.Feature,
				Title:       "Invite tracking isn't working",
				Description: "The bot couldn't read this server's invites, so it can't tell which invite new members use (and `/whoinvited` will show \"couldn't be determined\"). Give the bot the Manage Server permission, then restart it.",
				Fields:      []discord.EmbedField{alerts.ErrorField(err)},
			})
			return
		}

		cache.Set(e.GuildID, shared.Snapshot(invites))
		slog.Info("inviteTracker: invite cache initialized", "guild_id", e.GuildID, "invites", len(invites))
	})
}
