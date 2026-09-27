// Button handlers for confirming or cancelling an admin purge.
package components

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/shared"
)

// HandleConfirm returns the handler for the confirm button: re-check the
// clicker is an admin and purge is still enabled, then queue the purge.
func HandleConfirm(db *database.DB, purger *shared.Purger) handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		if !shared.IsAdmin(e.Member()) {
			return closePrompt(e, "❌ Only administrators can purge messages.")
		}
		userID, err := snowflake.Parse(e.Vars["user"])
		if err != nil {
			return closePrompt(e, "❌ This button is broken.")
		}
		guildID := *e.GuildID()

		enabled, err := shared.Enabled(e.Ctx, db, guildID)
		if err != nil {
			slog.Error("messagePurge: check enabled", "guild_id", guildID, "err", err)
			return closePrompt(e, "❌ Database error — please try again.")
		}
		if !enabled {
			return closePrompt(e, "❌ Message purge was disabled.")
		}

		requestedBy := e.User().ID
		id, existing, err := purger.Queue(e.Ctx, e.Client(), guildID, userID, "command", &requestedBy)
		if err != nil {
			slog.Error("messagePurge: queue purge", "guild_id", guildID, "user_id", userID, "err", err)
			return closePrompt(e, "❌ Couldn't start the purge — please try again.")
		}
		if existing {
			return closePrompt(e, fmt.Sprintf("A purge of <@%s>'s messages is already running (job #%d).", userID, id))
		}

		slog.Info("messagePurge: purge requested", "guild_id", guildID, "user_id", userID, "by", requestedBy, "id", id)
		return closePrompt(e, fmt.Sprintf("🗑️ Deleting <@%s>'s messages (job #%d). A summary will be posted to the admin alerts channel when it finishes.", userID, id))
	}
}

// HandleCancel returns the handler for the cancel button.
func HandleCancel() handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		return closePrompt(e, "Cancelled. No messages were deleted.")
	}
}

// closePrompt replaces the confirmation prompt with msg and removes its buttons.
func closePrompt(e *handler.ComponentEvent, msg string) error {
	return e.UpdateMessage(discord.MessageUpdate{
		Content:    &msg,
		Embeds:     &[]discord.Embed{},
		Components: &[]discord.LayoutComponent{},
	})
}
