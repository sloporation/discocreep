// Commands to configure which channels receive login/leave notifications.
package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// JLLCommand returns the /jll application command definition with four subcommands:
// - join-channel: channel for user join messages
// - admin-join-channel: channel for admin join messages (with invite code)
// - leave-channel: channel for user leave messages
// - admin-leave-channel: channel for admin leave messages
func JLLCommand() *discordgo.ApplicationCommand {
	adminPerm := int64(discordgo.PermissionAdministrator)
	return &discordgo.ApplicationCommand{
		Name:                     "jll",
		Description:              "Configure login loader channels",
		DefaultMemberPermissions: &adminPerm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "join-channel",
				Description: "Set the channel for user join notifications",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "channel",
						Description: "The channel to send join messages to",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "admin-join-channel",
				Description: "Set the channel for admin join notifications (with invite code)",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "channel",
						Description: "The channel to send admin join messages to",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "leave-channel",
				Description: "Set the channel for user leave notifications",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "channel",
						Description: "The channel to send leave messages to",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "admin-leave-channel",
				Description: "Set the channel for admin leave notifications",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "channel",
						Description: "The channel to send admin leave messages to",
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleJLL returns the interaction handler for the /jll command.
// It dispatches to the appropriate subcommand handler based on the interaction data.
func HandleJLL(db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		data := i.ApplicationCommandData()
		if len(data.Options) == 0 {
			respond(s, i, "❌ Unknown subcommand.")
			return
		}

		subcommand := data.Options[0].Name
		channelID := data.Options[0].Options[0].ChannelValue(s).ID
		guildID := i.GuildID

		// Map subcommand name to database column
		columnMap := map[string]string{
			"join-channel":       "join_channel_id",
			"admin-join-channel": "join_admin_channel_id",
			"leave-channel":      "leave_channel_id",
			"admin-leave-channel": "leave_admin_channel_id",
		}

		column, ok := columnMap[subcommand]
		if !ok {
			respond(s, i, "❌ Unknown subcommand.")
			return
		}

		// Update the guild setting in the database
		query := fmt.Sprintf("UPDATE guilds SET %s = ? WHERE id = ?", column)
		result, err := db.ExecContext(context.Background(), query, channelID, guildID)
		if err != nil {
			slog.Error("loginLogger: update channel", "column", column, "err", err)
			respond(s, i, "❌ Database error — please try again.")
			return
		}

		// If no rows were affected, insert the guild first
		rows, _ := result.RowsAffected()
		if rows == 0 {
			insertQuery := fmt.Sprintf("INSERT INTO guilds (id, %s) VALUES (?, ?)", column)
			if _, err := db.ExecContext(context.Background(), insertQuery, guildID, channelID); err != nil {
				slog.Error("loginLogger: insert guild", "err", err)
				respond(s, i, "❌ Database error — please try again.")
				return
			}
		}

		slog.Info("loginLogger: configured channel", "subcommand", subcommand, "channel_id", channelID, "guild_id", guildID)
		respond(s, i, fmt.Sprintf("✅ %s channel set to <#%s>", formatSubcommand(subcommand), channelID))
	}
}

// formatSubcommand converts "join-channel" to "Join" for display
func formatSubcommand(s string) string {
	switch s {
	case "join-channel":
		return "Join"
	case "admin-join-channel":
		return "Admin Join"
	case "leave-channel":
		return "Leave"
	case "admin-leave-channel":
		return "Admin Leave"
	default:
		return s
	}
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("loginLogger: respond", "err", err)
	}
}
