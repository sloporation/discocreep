// When a user leaves the discord, send messages to configured notification channels.
package events

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleMemberRemove returns a GuildMemberRemove handler that sends leave notifications
// to configured channels.
func HandleMemberRemove(db *database.DB) func(*discordgo.Session, *discordgo.GuildMemberRemove) {
	return func(s *discordgo.Session, e *discordgo.GuildMemberRemove) {
		// Get guild settings
		var leaveChannelID, leaveAdminChannelID *string
		err := db.QueryRowContext(context.Background(),
			"SELECT leave_channel_id, leave_admin_channel_id FROM guilds WHERE id = ?",
			e.GuildID,
		).Scan(&leaveChannelID, &leaveAdminChannelID)

		if err != nil && err != sql.ErrNoRows {
			slog.Warn("loginLogger: query guild settings", "guild_id", e.GuildID, "err", err)
			return
		}

		// No channels configured
		if leaveChannelID == nil && leaveAdminChannelID == nil {
			return
		}

		userInfo := fmt.Sprintf("%s (ID: %s)", e.User.Username, e.User.ID)
		timestamp := time.Now().Format(time.RFC3339)

		// Send to user leave channel if configured
		if leaveChannelID != nil {
			sendMessage(s, *leaveChannelID, discordgo.MessageEmbed{
				Color:       0xFF0000,
				Title:       "👋 User Left",
				Description: userInfo,
				Timestamp:   timestamp,
			})
		}

		// Send to admin leave channel if configured
		if leaveAdminChannelID != nil {
			sendMessage(s, *leaveAdminChannelID, discordgo.MessageEmbed{
				Color:       0xFF0000,
				Title:       "👋 User Left (Admin)",
				Description: userInfo,
				Timestamp:   timestamp,
			})
		}
	}
}
