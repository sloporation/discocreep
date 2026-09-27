// Commands to configure which channels receive login/leave notifications.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// columnMap maps each /jll subcommand to the guilds column it sets. The
// column names are interpolated into SQL, so they must only come from here.
var columnMap = map[string]string{
	"join-channel":        "join_channel_id",
	"admin-join-channel":  "join_admin_channel_id",
	"leave-channel":       "leave_channel_id",
	"admin-leave-channel": "leave_admin_channel_id",
}

// JLLCommand returns the /jll application command definition with four subcommands:
// - join-channel: channel for user join messages
// - admin-join-channel: channel for admin join messages (with invite code)
// - leave-channel: channel for user leave messages
// - admin-leave-channel: channel for admin leave messages
func JLLCommand() discord.ApplicationCommandCreate {
	subcommand := func(name, description, channelDescription string) discord.ApplicationCommandOptionSubCommand {
		return discord.ApplicationCommandOptionSubCommand{
			Name:        name,
			Description: description,
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionChannel{
					Name:        "channel",
					Description: channelDescription,
					Required:    true,
				},
			},
		}
	}

	return discord.SlashCommandCreate{
		Name:                     "jll",
		Description:              "Configure login loader channels",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionAdministrator),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			subcommand("join-channel", "Set the channel for user join notifications", "The channel to send join messages to"),
			subcommand("admin-join-channel", "Set the channel for admin join notifications (with invite code)", "The channel to send admin join messages to"),
			subcommand("leave-channel", "Set the channel for user leave notifications", "The channel to send leave messages to"),
			subcommand("admin-leave-channel", "Set the channel for admin leave notifications", "The channel to send admin leave messages to"),
		},
	}
}

// HandleJLL returns the handler for every /jll subcommand. Register it on
// "/jll" so it matches all of them.
func HandleJLL(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		if data.SubCommandName == nil {
			return respond(e, "❌ Unknown subcommand.")
		}
		subcommand := *data.SubCommandName

		column, ok := columnMap[subcommand]
		if !ok {
			return respond(e, "❌ Unknown subcommand.")
		}

		channelID := data.Snowflake("channel")
		guildID := *e.GuildID()

		// guilds.name is NOT NULL, so the row can't be created with just the ID.
		guildName := guildID.String()
		if g, ok := e.Guild(); ok {
			guildName = g.Name
		}

		// Upsert the guild row with the chosen channel.
		query := fmt.Sprintf(
			"INSERT INTO guilds (id, name, %[1]s) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE %[1]s = VALUES(%[1]s)",
			column,
		)
		if _, err := db.ExecContext(e.Ctx, query, guildID, guildName, channelID); err != nil {
			slog.Error("loginLogger: update channel", "column", column, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		slog.Info("loginLogger: configured channel", "subcommand", subcommand, "channel_id", channelID, "guild_id", guildID)
		return respond(e, fmt.Sprintf("✅ %s channel set to <#%s>", formatSubcommand(subcommand), channelID))
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
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
