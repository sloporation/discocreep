// Initialize the invite cache when the bot is ready.
package events

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger"
)

// HandleReady returns a Ready handler that initializes the invite cache for all guilds.
// This should be called once when the bot connects to Discord.
func HandleReady(s *discordgo.Session, cache *loginLogger.InviteCache) func(*discordgo.Session, *discordgo.Ready) {
	return func(sess *discordgo.Session, r *discordgo.Ready) {
		slog.Info("loginLogger: initializing invite cache")

		for _, guild := range r.Guilds {
			invites, err := sess.GuildInvites(guild.ID)
			if err != nil {
				slog.Warn("loginLogger: fetch invites", "guild_id", guild.ID, "err", err)
				continue
			}

			// Build invite map: code -> uses
			inviteMap := make(map[string]int)
			for _, invite := range invites {
				uses := 0
				if invite.Uses != nil {
					uses = *invite.Uses
				}
				inviteMap[invite.Code] = uses
			}

			cache.SetInvites(guild.ID, inviteMap)
		}

		slog.Info("loginLogger: invite cache initialized for", "guild_count", len(r.Guilds))
	}
}
