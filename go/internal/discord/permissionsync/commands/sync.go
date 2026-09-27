// Command to copy channel permissions from source to destination.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

// CopyPermissionsCommand returns the /copypermissions application command definition.
// Requires Manage Channels permission by default.
func CopyPermissionsCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "copypermissions",
		Description:              "Copy permissions from one channel to another",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageChannels),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionChannel{
				Name:        "source",
				Description: "The channel to copy permissions from",
				Required:    true,
			},
			discord.ApplicationCommandOptionChannel{
				Name:        "destination",
				Description: "The channel to copy permissions to",
				Required:    true,
			},
		},
	}
}

// HandleCopyPermissions returns the handler for the /copypermissions command.
func HandleCopyPermissions() handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		sourceChannelID := data.Snowflake("source")
		destChannelID := data.Snowflake("destination")

		// Respond with defer since this might take a moment
		if err := e.DeferCreateMessage(true); err != nil {
			return fmt.Errorf("defer interaction: %w", err)
		}

		sourceChannel, err := fetchGuildChannel(e, sourceChannelID)
		if err != nil {
			slog.Error("permissionsync: fetch source channel", "channel_id", sourceChannelID, "err", err)
			return respondEdit(e, "❌ Could not fetch source channel.")
		}

		destChannel, err := fetchGuildChannel(e, destChannelID)
		if err != nil {
			slog.Error("permissionsync: fetch dest channel", "channel_id", destChannelID, "err", err)
			return respondEdit(e, "❌ Could not fetch destination channel.")
		}

		// Verify both channels are in the same guild
		if sourceChannel.GuildID() != destChannel.GuildID() {
			return respondEdit(e, "❌ Source and destination channels must be in the same guild.")
		}

		// Replace the destination's overwrites with the source's in a single
		// PATCH, so the channel is never left half-copied. Only
		// permission_overwrites is sent, so this works for any guild channel
		// type despite the Text update struct.
		overwrites := []discord.PermissionOverwrite(sourceChannel.PermissionOverwrites())
		if overwrites == nil {
			overwrites = []discord.PermissionOverwrite{} // send [] to clear, not omit
		}
		if _, err := e.Client().Rest.UpdateChannel(destChannelID, discord.GuildTextChannelUpdate{
			PermissionOverwrites: &overwrites,
		}); err != nil {
			slog.Error("permissionsync: copy permissions", "err", err)
			return respondEdit(e, fmt.Sprintf("❌ Failed to copy permissions: %v", err))
		}

		slog.Info("permissionsync: copied permissions", "source", sourceChannelID, "dest", destChannelID, "guild_id", sourceChannel.GuildID())
		return respondEdit(e, fmt.Sprintf("✅ Copied permissions from <#%s> to <#%s>", sourceChannelID, destChannelID))
	}
}

// fetchGuildChannel returns the channel from cache, falling back to REST.
func fetchGuildChannel(e *handler.CommandEvent, id snowflake.ID) (discord.GuildChannel, error) {
	if ch, ok := e.Client().Caches.Channel(id); ok {
		return ch, nil
	}
	ch, err := e.Client().Rest.GetChannel(id)
	if err != nil {
		return nil, err
	}
	gc, ok := ch.(discord.GuildChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s is not a guild channel", id)
	}
	return gc, nil
}

// respondEdit edits the deferred interaction response.
func respondEdit(e *handler.CommandEvent, msg string) error {
	_, err := e.UpdateInteractionResponse(discord.MessageUpdate{Content: &msg})
	return err
}
