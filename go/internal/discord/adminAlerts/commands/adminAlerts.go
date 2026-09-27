// Commands to configure the server-wide admin alerts channel.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
)

// AdminAlertsCommand returns the /adminalerts application command definition
// with set and clear subcommands. Administrator only by default.
func AdminAlertsCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "adminalerts",
		Description:              "Configure where the bot reports problems that need an admin",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionAdministrator),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Set the admin alerts channel",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "Channel for admin alerts (keep it private to admins)",
						Required:     true,
						ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "clear",
				Description: "Stop posting admin alerts",
			},
		},
	}
}

// HandleSet returns the handler for /adminalerts set. It posts a confirmation
// in the channel first, so a missing permission is caught now rather than
// when the first real alert fails.
func HandleSet(a *alerts.Alerter) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		guildID := *e.GuildID()
		channelID := data.Snowflake("channel")

		if _, err := e.Client().Rest.CreateMessage(channelID, discord.MessageCreate{
			Embeds: []discord.Embed{{
				Color:       0x57F287,
				Title:       "Admin alerts",
				Description: fmt.Sprintf("<@%s> set this as the admin alerts channel. The bot will post here when something needs an admin's attention.", e.User().ID),
			}},
		}); err != nil {
			slog.Warn("adminAlerts: post confirmation", "guild_id", guildID, "channel_id", channelID, "err", err)
			return respond(e, fmt.Sprintf("❌ The bot couldn't post in <#%s>. Give it View Channel, Send Messages and Embed Links there, then try again.", channelID))
		}

		guildName := guildID.String()
		if g, ok := e.Guild(); ok {
			guildName = g.Name
		}
		if err := a.SetChannel(e.Ctx, guildID, guildName, &channelID); err != nil {
			slog.Error("adminAlerts: save channel", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		slog.Info("adminAlerts: channel set", "guild_id", guildID, "channel_id", channelID)
		return respond(e, fmt.Sprintf("✅ Admin alerts will be posted in <#%s>.", channelID))
	}
}

// HandleClear returns the handler for /adminalerts clear.
func HandleClear(a *alerts.Alerter) handler.SlashCommandHandler {
	return func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		guildID := *e.GuildID()

		guildName := guildID.String()
		if g, ok := e.Guild(); ok {
			guildName = g.Name
		}
		if err := a.SetChannel(e.Ctx, guildID, guildName, nil); err != nil {
			slog.Error("adminAlerts: clear channel", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		slog.Info("adminAlerts: channel cleared", "guild_id", guildID)
		return respond(e, "✅ Admin alerts channel cleared. Problems will only be logged.")
	}
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
