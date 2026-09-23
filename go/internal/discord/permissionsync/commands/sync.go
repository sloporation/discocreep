// Command to copy channel permissions from source to destination.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

// CopyPermissionsCommand returns the /copypermissions application command definition.
// Requires Manage Channels permission by default.
func CopyPermissionsCommand() *discordgo.ApplicationCommand {
	manageChannels := int64(discordgo.PermissionManageChannels)
	return &discordgo.ApplicationCommand{
		Name:                     "copypermissions",
		Description:              "Copy permissions from one channel to another",
		DefaultMemberPermissions: &manageChannels,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "source",
				Description: "The channel to copy permissions from",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionChannel,
				Name:        "destination",
				Description: "The channel to copy permissions to",
				Required:    true,
			},
		},
	}
}

// HandleCopyPermissions returns the interaction handler for the /copypermissions command.
func HandleCopyPermissions(s *discordgo.Session) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(sess *discordgo.Session, i *discordgo.InteractionCreate) {
		data := i.ApplicationCommandData()
		options := data.Options

		// Get source and destination channel IDs
		sourceChannelID := options[0].ChannelValue(sess).ID
		destChannelID := options[1].ChannelValue(sess).ID

		// Respond with defer since this might take a moment
		if err := sess.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags: discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			slog.Error("permissionsync: defer interaction", "err", err)
			return
		}

		// Fetch source channel permissions
		sourceChannel, err := sess.Channel(sourceChannelID)
		if err != nil {
			slog.Error("permissionsync: fetch source channel", "channel_id", sourceChannelID, "err", err)
			respondEdit(sess, i, "❌ Could not fetch source channel.")
			return
		}

		// Fetch destination channel
		destChannel, err := sess.Channel(destChannelID)
		if err != nil {
			slog.Error("permissionsync: fetch dest channel", "channel_id", destChannelID, "err", err)
			respondEdit(sess, i, "❌ Could not fetch destination channel.")
			return
		}

		// Verify both channels are in the same guild
		if sourceChannel.GuildID != destChannel.GuildID {
			respondEdit(sess, i, "❌ Source and destination channels must be in the same guild.")
			return
		}

		// Copy permissions
		if err := copyPermissions(sess, sourceChannel, destChannel); err != nil {
			slog.Error("permissionsync: copy permissions", "err", err)
			respondEdit(sess, i, fmt.Sprintf("❌ Failed to copy permissions: %v", err))
			return
		}

		slog.Info("permissionsync: copied permissions", "source", sourceChannelID, "dest", destChannelID, "guild_id", sourceChannel.GuildID)
		respondEdit(sess, i, fmt.Sprintf("✅ Copied permissions from <#%s> to <#%s>", sourceChannelID, destChannelID))
	}
}

// copyPermissions copies all permission overwrites from source to destination channel.
func copyPermissions(s *discordgo.Session, source *discordgo.Channel, dest *discordgo.Channel) error {
	// Delete all existing permission overwrites on destination
	for _, overwrite := range dest.PermissionOverwrites {
		if err := s.ChannelPermissionDelete(dest.ID, overwrite.ID); err != nil {
			return fmt.Errorf("delete overwrite %s: %w", overwrite.ID, err)
		}
	}

	// Copy all permission overwrites from source to destination
	for _, overwrite := range source.PermissionOverwrites {
		// Create permission overwrite with the same Allow/Deny as source
		if err := s.ChannelPermissionSet(dest.ID, overwrite.ID, overwrite.Type, overwrite.Allow, overwrite.Deny); err != nil {
			return fmt.Errorf("set overwrite %s: %w", overwrite.ID, err)
		}
	}

	return nil
}

// respondEdit sends a deferred interaction follow-up message.
func respondEdit(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &msg,
	}); err != nil {
		slog.Error("permissionsync: respond edit", "err", err)
	}
}
