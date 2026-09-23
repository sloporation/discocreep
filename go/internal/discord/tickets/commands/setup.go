// Command to set up the ticket system for a guild.
package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets"
)

// TicketCommand returns the /ticket application command definition.
func TicketCommand() *discordgo.ApplicationCommand {
	adminPerm := int64(discordgo.PermissionAdministrator)
	return &discordgo.ApplicationCommand{
		Name:                     "ticket",
		Description:              "Manage the ticket system",
		DefaultMemberPermissions: &adminPerm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "setup",
				Description: "Set up the ticket system",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "channel",
						Description: "The channel to post the ticket creation button",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionChannel,
						Name:        "archive",
						Description: "The category to move closed tickets to",
						Required:    true,
						ChannelTypes: []discordgo.ChannelType{
							discordgo.ChannelTypeGuildCategory,
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionRole,
						Name:        "role",
						Description: "The role to notify and give permissions for tickets",
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleTicket returns the interaction handler for the /ticket command.
func HandleTicket(s *discordgo.Session, db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(sess *discordgo.Session, i *discordgo.InteractionCreate) {
		data := i.ApplicationCommandData()
		if len(data.Options) == 0 {
			respond(sess, i, "❌ Unknown subcommand.")
			return
		}

		subcommand := data.Options[0].Name
		switch subcommand {
		case "setup":
			handleSetup(sess, i, data.Options[0], db)
		default:
			respond(sess, i, "❌ Unknown subcommand.")
		}
	}
}

func handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption, db *database.DB) {
	guildID := i.GuildID
	options := sub.Options

	// Parse options
	supportChannelID := options[0].ChannelValue(s).ID
	archiveCategoryID := options[1].ChannelValue(s).ID
	notifyRoleID := options[2].RoleValue(s).ID

	// Verify the support channel is a text channel
	supportChannel, err := s.Channel(supportChannelID)
	if err != nil {
		slog.Error("tickets: fetch support channel", "err", err)
		respond(s, i, "❌ Could not fetch support channel.")
		return
	}
	if supportChannel.Type != discordgo.ChannelTypeGuildText {
		respond(s, i, "❌ Support channel must be a text channel.")
		return
	}

	// Verify archive is a category
	archiveChannel, err := s.Channel(archiveCategoryID)
	if err != nil {
		slog.Error("tickets: fetch archive category", "err", err)
		respond(s, i, "❌ Could not fetch archive category.")
		return
	}
	if archiveChannel.Type != discordgo.ChannelTypeGuildCategory {
		respond(s, i, "❌ Archive must be a category.")
		return
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
	if _, err := db.ExecContext(context.Background(), query,
		guildID, supportChannelID, archiveCategoryID, notifyRoleID,
	); err != nil {
		slog.Error("tickets: save config", "err", err)
		respond(s, i, "❌ Database error — please try again.")
		return
	}

	// Post the ticket creation message in the support channel
	embed := &discordgo.MessageEmbed{
		Color:       0x5865F2,
		Title:       "📋 Support Tickets",
		Description: "Click the button below to create a support ticket.",
	}

	button := &discordgo.Button{
		Label:    "Create Ticket",
		Style:    discordgo.PrimaryButton,
		CustomID: tickets.CreateTicketButtonID,
		Emoji: &discordgo.ComponentEmoji{
			Name: "📝",
		},
	}

	if _, err := s.ChannelMessageSendComplex(supportChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{button},
			},
		},
	}); err != nil {
		slog.Error("tickets: send setup message", "err", err)
		respond(s, i, "⚠️ Setup saved but failed to post message in support channel.")
		return
	}

	slog.Info("tickets: setup complete", "guild_id", guildID, "support_channel", supportChannelID, "archive_category", archiveCategoryID, "role", notifyRoleID)
	respond(s, i, fmt.Sprintf("✅ Ticket system configured! Support channel: <#%s>, Archive: <#%s>, Role: <@&%s>", supportChannelID, archiveCategoryID, notifyRoleID))
}

// respond sends an ephemeral reply.
func respond(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		slog.Error("tickets: respond", "err", err)
	}
}
