// When a user leaves the discord, send messages to configured notification channels.
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

// HandleMemberLeave returns a GuildMemberLeave listener that sends leave
// notifications to configured channels.
func HandleMemberLeave(db *database.DB, alerter *alerts.Alerter) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberLeave) {
		// Get guild settings
		var leaveChannelID, leaveAdminChannelID *snowflake.ID
		err := db.QueryRowContext(context.Background(),
			"SELECT leave_channel_id, leave_admin_channel_id FROM guilds WHERE id = ?",
			e.GuildID,
		).Scan(&leaveChannelID, &leaveAdminChannelID)

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("loginLogger: query guild settings", "guild_id", e.GuildID, "err", err)
			return
		}

		// No channels configured
		if leaveChannelID == nil && leaveAdminChannelID == nil {
			return
		}

		userInfo := fmt.Sprintf("%s (ID: %s)", e.User.Username, e.User.ID)
		now := time.Now()

		// Send to user leave channel if configured
		if leaveChannelID != nil {
			sendMessage(e.Client(), alerter, e.GuildID, *leaveChannelID, discord.Embed{
				Color:       0xFF0000,
				Title:       "👋 User Left",
				Description: userInfo,
				Timestamp:   &now,
			})
		}

		// Send to admin leave channel if configured
		if leaveAdminChannelID != nil {
			sendMessage(e.Client(), alerter, e.GuildID, *leaveAdminChannelID, discord.Embed{
				Color:       0xFF0000,
				Title:       "👋 User Left (Admin)",
				Description: userInfo,
				Timestamp:   &now,
			})
		}
	})
}
