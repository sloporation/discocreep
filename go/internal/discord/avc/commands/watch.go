// Command to configure which voice channel to monitor.
// The admin can specify a voice channel to watch (/avc watch) or unwatch
// (/avc unwatch). New channels created by the bot will appear in the same
// category as the watched channel, or at the top level if it has none.
package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// AVCCommand returns the /avc application command definition with watch and
// unwatch subcommands. Requires Manage Channels permission by default.
func AVCCommand() *discordgo.ApplicationCommand {
	manageChannels := int64(discordgo.PermissionManageChannels)
	return &discordgo.ApplicationCommand{
		Name:                     "avc",
		Description:              "Auto voice channel configuration",
		DefaultMemberPermissions: &manageChannels,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "watch",
				Description: "Monitor a voice channel and create personal channels when users join it",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionChannel,
						Name:         "channel",
						Description:  "The voice channel to monitor",
						Required:     true,
						ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "unwatch",
				Description: "Stop monitoring a voice channel",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionChannel,
						Name:         "channel",
						Description:  "The voice channel to stop monitoring",
						Required:     true,
						ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildVoice},
					},
				},
			},
		},
	}
}

// HandleAVC returns the interaction handler for the /avc command. It dispatches
// to the watch or unwatch subcommand based on the interaction data.
func HandleAVC(db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		options := i.ApplicationCommandData().Options
		if len(options) == 0 {
			respond(s, i, "Unknown subcommand.")
			return
		}

		switch options[0].Name {
		case "watch":
			handleWatch(db, s, i, options[0])
		case "unwatch":
			handleUnwatch(db, s, i, options[0])
		default:
			respond(s, i, "Unknown subcommand.")
		}
	}
}

func handleWatch(db *database.DB, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	channelID := sub.Options[0].ChannelValue(s).ID
	guildID := i.GuildID

	// Check if already monitored.
	var count int
	if err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM avc_monitors WHERE channel_id = ?",
		channelID,
	).Scan(&count); err != nil {
		slog.Error("avc watch: db check", "err", err)
		respond(s, i, "Database error — please try again.")
		return
	}
	if count > 0 {
		respond(s, i, fmt.Sprintf("<#%s> is already being monitored.", channelID))
		return
	}

	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO avc_monitors (channel_id, guild_id) VALUES (?, ?)",
		channelID, guildID,
	); err != nil {
		slog.Error("avc watch: db insert", "channel_id", channelID, "err", err)
		respond(s, i, "Database error — please try again.")
		return
	}

	slog.Info("avc: watching channel", "channel_id", channelID, "guild_id", guildID)
	respond(s, i, fmt.Sprintf("Now watching <#%s>. Users who join it will get their own voice channel.", channelID))
}

func handleUnwatch(db *database.DB, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	channelID := sub.Options[0].ChannelValue(s).ID

	result, err := db.ExecContext(context.Background(),
		"DELETE FROM avc_monitors WHERE channel_id = ? AND guild_id = ?",
		channelID, i.GuildID,
	)
	if err != nil {
		slog.Error("avc unwatch: db delete", "channel_id", channelID, "err", err)
		respond(s, i, "Database error — please try again.")
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respond(s, i, fmt.Sprintf("<#%s> was not being monitored.", channelID))
		return
	}

	slog.Info("avc: unwatched channel", "channel_id", channelID, "guild_id", i.GuildID)
	respond(s, i, fmt.Sprintf("Stopped watching <#%s>.", channelID))
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
		slog.Error("avc: interaction respond", "err", err)
	}
}
