// When a user joins the discord, send messages to configured notification channels.
// Admin channel receives the invite code and creator information.
package events

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger"
)

// HandleMemberAdd returns a GuildMemberAdd handler that sends join notifications
// to configured channels and tracks the invite that was used.
func HandleMemberAdd(db *database.DB, cache *loginLogger.InviteCache) func(*discordgo.Session, *discordgo.GuildMemberAdd) {
	return func(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
		// Get guild settings
		var joinChannelID, joinAdminChannelID *string
		err := db.QueryRowContext(context.Background(),
			"SELECT join_channel_id, join_admin_channel_id FROM guilds WHERE id = ?",
			e.GuildID,
		).Scan(&joinChannelID, &joinAdminChannelID)

		if err != nil && err != sql.ErrNoRows {
			slog.Warn("loginLogger: query guild settings", "guild_id", e.GuildID, "err", err)
			return
		}

		// No channels configured
		if joinChannelID == nil && joinAdminChannelID == nil {
			return
		}

		userInfo := fmt.Sprintf("%s (ID: %s)", e.User.Username, e.User.ID)
		timestamp := time.Now().Format(time.RFC3339)

		// Send to user join channel if configured
		if joinChannelID != nil {
			sendMessage(s, *joinChannelID, discordgo.MessageEmbed{
				Color:       0x00FF00,
				Title:       "👋 User Joined",
				Description: userInfo,
				Timestamp:   timestamp,
			})
		}

		// Send to admin join channel if configured
		if joinAdminChannelID != nil {
			// Find the invite that was used
			inviteInfo := findUsedInvite(s, e.GuildID, cache)

			embed := discordgo.MessageEmbed{
				Color:       0x00FF00,
				Title:       "👋 User Joined (Admin)",
				Description: userInfo,
				Timestamp:   timestamp,
			}

			if inviteInfo != nil {
				embed.Fields = []*discordgo.MessageEmbedField{
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

			sendMessage(s, *joinAdminChannelID, embed)
		}
	}
}

// inviteInfo holds details about which invite was used
type inviteInfo struct {
	Code       string
	InviterID  string
	InviterName string
}

// findUsedInvite compares the cached invites with current invites to find
// which one was just used. Returns nil if unable to determine.
func findUsedInvite(s *discordgo.Session, guildID string, cache *loginLogger.InviteCache) *inviteInfo {
	// Get current invites
	invites, err := s.GuildInvites(guildID)
	if err != nil {
		slog.Error("loginLogger: fetch invites", "guild_id", guildID, "err", err)
		return nil
	}

	// Get cached invites
	oldInvites := cache.GetInvites(guildID)

	// Build new cache map
	newInviteMap := make(map[string]int)
	var used *inviteInfo

	for _, invite := range invites {
		uses := 0
		if invite.Uses != nil {
			uses = *invite.Uses
		}
		newInviteMap[invite.Code] = uses

		// Check if this invite's uses increased
		if oldUses, exists := oldInvites[invite.Code]; exists {
			if uses > oldUses {
				// This is the invite that was used
				inviterName := "Unknown"
				if invite.Inviter != nil {
					inviterName = invite.Inviter.Username
				}
				used = &inviteInfo{
					Code:        invite.Code,
					InviterID:   invite.Inviter.ID,
					InviterName: inviterName,
				}
			}
		} else if uses > 0 {
			// New invite that wasn't cached before, but has uses
			inviterName := "Unknown"
			if invite.Inviter != nil {
				inviterName = invite.Inviter.Username
			}
			used = &inviteInfo{
				Code:        invite.Code,
				InviterID:   invite.Inviter.ID,
				InviterName: inviterName,
			}
		}
	}

	// Update cache with new invites
	cache.SetInvites(guildID, newInviteMap)

	return used
}

// sendMessage sends an embed to a text channel
func sendMessage(s *discordgo.Session, channelID string, embed discordgo.MessageEmbed) {
	_, err := s.ChannelMessageSendEmbed(channelID, &embed)
	if err != nil {
		slog.Error("loginLogger: send message", "channel_id", channelID, "err", err)
	}
}
