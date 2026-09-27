// Command to set up the ticket system for a guild.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/shared"
)

// TicketCommand returns the /ticket application command definition.
func TicketCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "ticket",
		Description:              "Manage the ticket system",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionAdministrator),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "setup",
				Description: "Set up the ticket system",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "The channel to post the ticket creation button",
						Required:     true,
						ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText},
					},
					discord.ApplicationCommandOptionChannel{
						Name:         "archive",
						Description:  "The category to move closed tickets to",
						Required:     true,
						ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildCategory},
					},
					discord.ApplicationCommandOptionRole{
						Name:        "role",
						Description: "The role to notify and give permissions for tickets",
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleSetup returns the handler for /ticket setup.
func HandleSetup(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		guildID := *e.GuildID()

		// Parse options. Resolved channels carry their type, so no fetch is needed.
		supportChannel := data.Channel("channel")
		archiveChannel := data.Channel("archive")
		notifyRoleID := data.Snowflake("role")

		// Discord already restricts the choices via ChannelTypes; double-check anyway.
		if supportChannel.Type != discord.ChannelTypeGuildText {
			return respond(e, "❌ Support channel must be a text channel.")
		}
		if archiveChannel.Type != discord.ChannelTypeGuildCategory {
			return respond(e, "❌ Archive must be a category.")
		}

		// Save configuration to database
		query := `
			INSERT INTO ticket_configs (guild_id, support_channel_id, archive_category_id, notify_role_id)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
			support_channel_id = VALUES(support_channel_id),
			archive_category_id = VALUES(archive_category_id),
			notify_role_id = VALUES(notify_role_id)
		`
		if _, err := db.ExecContext(e.Ctx, query,
			guildID, supportChannel.ID, archiveChannel.ID, notifyRoleID,
		); err != nil {
			slog.Error("tickets: save config", "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		// Post the ticket creation message in the support channel
		if _, err := e.Client().Rest.CreateMessage(supportChannel.ID, discord.MessageCreate{
			Embeds: []discord.Embed{{
				Color:       0x5865F2,
				Title:       "📋 Support Tickets",
				Description: "Click the button below to create a support ticket.",
			}},
			Components: []discord.LayoutComponent{
				discord.NewActionRow(
					discord.NewPrimaryButton("Create Ticket", shared.CreateTicketButtonID).
						WithEmoji(discord.NewComponentEmoji("📝")),
				),
			},
		}); err != nil {
			slog.Error("tickets: send setup message", "err", err)
			return respond(e, "⚠️ Setup saved but failed to post message in support channel.")
		}

		slog.Info("tickets: setup complete", "guild_id", guildID, "support_channel", supportChannel.ID, "archive_category", archiveChannel.ID, "role", notifyRoleID)
		return respond(e, fmt.Sprintf("✅ Ticket system configured! Support channel: <#%s>, Archive: <#%s>, Role: <@&%s>", supportChannel.ID, archiveChannel.ID, notifyRoleID))
	}
}

// respond sends an ephemeral reply.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
