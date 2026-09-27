// Initialize the invite cache when the bot is ready.
package events

import (
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/shared"
)

// HandleGuildReady returns a listener that seeds the invite cache for each
// guild as it becomes available after connecting.
func HandleGuildReady(cache *shared.InviteCache) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildReady) {
		invites, err := e.Client().Rest.GetGuildInvites(e.GuildID)
		if err != nil {
			slog.Warn("loginLogger: fetch invites", "guild_id", e.GuildID, "err", err)
			return
		}

		// Build invite map: code -> uses
		inviteMap := make(map[string]int, len(invites))
		for _, invite := range invites {
			inviteMap[invite.Code] = invite.Uses
		}

		cache.SetInvites(e.GuildID, inviteMap)
		slog.Info("loginLogger: invite cache initialized", "guild_id", e.GuildID, "invites", len(inviteMap))
	})
}
