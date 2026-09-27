// When a user joins the discord, send messages to configured notification channels.
// Admin channel receives the invite code and creator information.
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

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/shared"
)

// HandleMemberJoin returns a GuildMemberJoin listener that sends join
// notifications to configured channels and tracks the invite that was used.
func HandleMemberJoin(db *database.DB, cache *shared.InviteCache) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberJoin) {
		// Get guild settings
		var joinChannelID, joinAdminChannelID *snowflake.ID
		err := db.QueryRowContext(context.Background(),
			"SELECT join_channel_id, join_admin_channel_id FROM guilds WHERE id = ?",
			e.GuildID,
		).Scan(&joinChannelID, &joinAdminChannelID)

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("loginLogger: query guild settings", "guild_id", e.GuildID, "err", err)
			return
		}

		// No channels configured
		if joinChannelID == nil && joinAdminChannelID == nil {
			return
		}

		user := e.Member.User
		userInfo := fmt.Sprintf("%s (ID: %s)", user.Username, user.ID)
		now := time.Now()

		// Send to user join channel if configured
		if joinChannelID != nil {
			sendMessage(e.Client(), *joinChannelID, discord.Embed{
				Color:       0x00FF00,
				Title:       "👋 User Joined",
				Description: userInfo,
				Timestamp:   &now,
			})
		}

		// Send to admin join channel if configured
		if joinAdminChannelID != nil {
			// Find the invite that was used
			inviteInfo := findUsedInvite(e.Client(), e.GuildID, cache)

			embed := discord.Embed{
				Color:       0x00FF00,
				Title:       "👋 User Joined (Admin)",
				Description: userInfo,
				Timestamp:   &now,
			}

			if inviteInfo != nil {
				embed.Fields = []discord.EmbedField{
					{
						Name:  "Invite Code",
						Value: inviteInfo.Code,
					},
					{
						Name:  "Invited By",
						Value: inviteInfo.InviterName,
					},
				}
			}

			sendMessage(e.Client(), *joinAdminChannelID, embed)
		}
	})
}

// inviteInfo holds details about which invite was used
type inviteInfo struct {
	Code        string
	InviterName string
}

// findUsedInvite compares the cached invites with current invites to find
// which one was just used. Returns nil if unable to determine.
func findUsedInvite(client *bot.Client, guildID snowflake.ID, cache *shared.InviteCache) *inviteInfo {
	// Get current invites
	invites, err := client.Rest.GetGuildInvites(guildID)
	if err != nil {
		slog.Error("loginLogger: fetch invites", "guild_id", guildID, "err", err)
		return nil
	}

	// Get cached invites
	oldInvites := cache.GetInvites(guildID)

	// Build new cache map
	newInviteMap := make(map[string]int, len(invites))
	var used *inviteInfo

	for _, invite := range invites {
		newInviteMap[invite.Code] = invite.Uses

		// An invite was used if its uses went up, or it is new since the last
		// snapshot and already has uses.
		oldUses, exists := oldInvites[invite.Code]
		if (exists && invite.Uses > oldUses) || (!exists && invite.Uses > 0) {
			inviterName := "Unknown"
			if invite.Inviter != nil {
				inviterName = invite.Inviter.Username
			}
			used = &inviteInfo{
				Code:        invite.Code,
				InviterName: inviterName,
			}
		}
	}

	// Update cache with new invites
	cache.SetInvites(guildID, newInviteMap)

	return used
}

// sendMessage sends an embed to a text channel
func sendMessage(client *bot.Client, channelID snowflake.ID, embed discord.Embed) {
	if _, err := client.Rest.CreateMessage(channelID, discord.MessageCreate{
		Embeds: []discord.Embed{embed},
	}); err != nil {
		slog.Error("loginLogger: send message", "channel_id", channelID, "err", err)
	}
}
