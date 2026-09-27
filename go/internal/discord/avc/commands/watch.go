// Command to configure which voice channel to monitor.
// The admin can specify a voice channel to watch (/avc watch) or unwatch
// (/avc unwatch). New channels created by the bot will appear in the same
// category as the watched channel, or at the top level if it has none.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// AVCCommand returns the /avc application command definition with watch and
// unwatch subcommands. Requires Manage Channels permission by default.
func AVCCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "avc",
		Description:              "Auto voice channel configuration",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageChannels),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "watch",
				Description: "Monitor a voice channel and create personal channels when users join it",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "The voice channel to monitor",
						Required:     true,
						ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildVoice},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "unwatch",
				Description: "Stop monitoring a voice channel",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "The voice channel to stop monitoring",
						Required:     true,
						ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildVoice},
					},
				},
			},
		},
	}
}

// HandleWatch returns the handler for /avc watch.
func HandleWatch(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		channelID := data.Snowflake("channel")
		guildID := *e.GuildID()

		// Check if already monitored.
		var count int
		if err := db.QueryRowContext(e.Ctx,
			"SELECT COUNT(*) FROM avc_monitors WHERE channel_id = ?",
			channelID,
		).Scan(&count); err != nil {
			slog.Error("avc watch: db check", "err", err)
			return respond(e, "Database error — please try again.")
		}
		if count > 0 {
			return respond(e, fmt.Sprintf("<#%s> is already being monitored.", channelID))
		}

		if _, err := db.ExecContext(e.Ctx,
			"INSERT INTO avc_monitors (channel_id, guild_id) VALUES (?, ?)",
			channelID, guildID,
		); err != nil {
			slog.Error("avc watch: db insert", "channel_id", channelID, "err", err)
			return respond(e, "Database error — please try again.")
		}

		slog.Info("avc: watching channel", "channel_id", channelID, "guild_id", guildID)
		return respond(e, fmt.Sprintf("Now watching <#%s>. Users who join it will get their own voice channel.", channelID))
	}
}

// HandleUnwatch returns the handler for /avc unwatch.
func HandleUnwatch(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		channelID := data.Snowflake("channel")
		guildID := *e.GuildID()

		result, err := db.ExecContext(e.Ctx,
			"DELETE FROM avc_monitors WHERE channel_id = ? AND guild_id = ?",
			channelID, guildID,
		)
		if err != nil {
			slog.Error("avc unwatch: db delete", "channel_id", channelID, "err", err)
			return respond(e, "Database error — please try again.")
		}

		rows, _ := result.RowsAffected()
		if rows == 0 {
			return respond(e, fmt.Sprintf("<#%s> was not being monitored.", channelID))
		}

		slog.Info("avc: unwatched channel", "channel_id", channelID, "guild_id", guildID)
		return respond(e, fmt.Sprintf("Stopped watching <#%s>.", channelID))
	}
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
