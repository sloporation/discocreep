// Seed the invite cache when each guild becomes available.
package events

import (
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/shared"
)

// HandleGuildReady returns a listener that snapshots each guild's invites
// after connecting, so the first join can be diffed against it.
func HandleGuildReady(cache *shared.InviteCache) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildReady) {
		invites, err := e.Client().Rest.GetGuildInvites(e.GuildID)
		if err != nil {
			slog.Warn("inviteTracker: fetch invites", "guild_id", e.GuildID, "err", err)
			return
		}

		cache.Set(e.GuildID, shared.Snapshot(invites))
		slog.Info("inviteTracker: invite cache initialized", "guild_id", e.GuildID, "invites", len(invites))
	})
}
