// Modal handler for renaming an auto voice channel.
package components

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/shared"
)

// HandleRenameModal returns the handler for the rename modal submission.
func HandleRenameModal(db *database.DB) handler.ModalHandler {
	return func(e *handler.ModalEvent) error {
		channelID := e.Channel().ID()
		if ok, err := checkOwner(e, db, channelID); !ok {
			return err
		}

		name := strings.TrimSpace(e.Data.Text(shared.RenameInputID))
		if name == "" {
			return ephemeral(e, "❌ The name can't be empty.")
		}

		// Discord limits channel renames to 2 per 10 minutes; if we hit that,
		// the REST client waits it out, so defer first.
		if err := e.DeferCreateMessage(true); err != nil {
			return fmt.Errorf("defer rename: %w", err)
		}

		if _, err := e.Client().Rest.UpdateChannel(channelID, discord.GuildVoiceChannelUpdate{Name: &name}); err != nil {
			slog.Error("avc: rename channel", "channel_id", channelID, "err", err)
			return respondEdit(e, "❌ Failed to rename the channel.")
		}

		slog.Info("avc: renamed channel", "channel_id", channelID, "name", name)
		return respondEdit(e, fmt.Sprintf("✏️ Renamed to **%s**.", name))
	}
}
