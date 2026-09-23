// Modal handler for ticket creation.
package components

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets"
)

// HandleTicketModal returns the modal submission handler for ticket creation.
func HandleTicketModal(s *discordgo.Session, db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(sess *discordgo.Session, i *discordgo.InteractionCreate) {
		// Defer the response
		if err := sess.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		}); err != nil {
			slog.Error("tickets: defer modal interaction", "err", err)
			return
		}

		data := i.ModalSubmitData()

		// Parse modal data
		var category, description string
		for _, row := range data.Components {
			if actionRow, ok := row.(*discordgo.ActionsRow); ok {
				for _, component := range actionRow.Components {
					switch c := component.(type) {
					case *discordgo.SelectMenu:
						if len(c.Values) > 0 {
							category = c.Values[0]
						}
					case *discordgo.TextInput:
						description = c.Value
					}
				}
			}
		}

		if category == "" || description == "" {
			respondEdit(sess, i, "❌ Please fill in all fields.")
			return
		}

		// Get guild config
		var notifyRoleID, supportChannelID string
		err := db.QueryRowContext(context.Background(),
			"SELECT notify_role_id, support_channel_id FROM ticket_configs WHERE guild_id = ?",
			i.GuildID,
		).Scan(&notifyRoleID, &supportChannelID)
		if err != nil {
			slog.Error("tickets: fetch config", "err", err)
			respondEdit(sess, i, "❌ Ticket system not configured.")
			return
		}

		// Get the support channel to determine the parent category
		supportChannel, err := sess.Channel(supportChannelID)
		if err != nil {
			slog.Error("tickets: fetch support channel", "err", err)
			respondEdit(sess, i, "❌ Could not fetch support channel.")
			return
		}

		// Get next ticket number by counting tickets in this category
		var ticketNumber int
		err = db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) + 1 FROM tickets WHERE guild_id = ? AND category = ?",
			i.GuildID, category,
		).Scan(&ticketNumber)
		if err != nil {
			ticketNumber = 1
		}

		// Build channel name: ticket-{number}-{category}
		channelName := fmt.Sprintf("ticket-%d-%s", ticketNumber, category)

		// Create the ticket channel
		channel, err := sess.GuildChannelCreateComplex(i.GuildID, discordgo.GuildChannelCreateData{
			Name:     channelName,
			Type:     discordgo.ChannelTypeGuildText,
			ParentID: supportChannel.ParentID, // Same parent as support channel
		})
		if err != nil {
			slog.Error("tickets: create channel", "err", err)
			respondEdit(sess, i, "❌ Failed to create ticket channel.")
			return
		}

		// Set permissions: creator + notify role can see and send messages
		creator := i.Member

		// Creator permissions
		if err := sess.ChannelPermissionSet(channel.ID, creator.User.ID, discordgo.PermissionOverwriteTypeMember,
			discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory,
			0); err != nil {
			slog.Error("tickets: set creator permissions", "err", err)
		}

		// Notify role permissions
		if err := sess.ChannelPermissionSet(channel.ID, notifyRoleID, discordgo.PermissionOverwriteTypeRole,
			discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionReadMessageHistory,
			0); err != nil {
			slog.Error("tickets: set role permissions", "err", err)
		}

		// Hide from @everyone
		if err := sess.ChannelPermissionSet(channel.ID, i.GuildID, discordgo.PermissionOverwriteTypeRole,
			0, discordgo.PermissionViewChannel); err != nil {
			slog.Error("tickets: hide from everyone", "err", err)
		}

		// Save ticket to database
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO tickets (channel_id, guild_id, creator_id, category, description) VALUES (?, ?, ?, ?, ?)",
			channel.ID, i.GuildID, creator.User.ID, category, description,
		); err != nil {
			slog.Error("tickets: save ticket", "err", err)
		}

		// Send initial message with close button
		embed := &discordgo.MessageEmbed{
			Color:       0x5865F2,
			Title:       fmt.Sprintf("Ticket #%d - %s", ticketNumber, formatCategory(category)),
			Description: description,
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:  "Creator",
					Value: fmt.Sprintf("<@%s>", creator.User.ID),
				},
				{
					Name:  "Support Team",
					Value: fmt.Sprintf("<@&%s>", notifyRoleID),
				},
			},
		}

		button := &discordgo.Button{
			Label:    "Close Ticket",
			Style:    discordgo.DangerButton,
			CustomID: tickets.CloseTicketButtonID,
			Emoji: &discordgo.ComponentEmoji{
				Name: "🔒",
			},
		}

		_, err = sess.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{button},
				},
			},
		})
		if err != nil {
			slog.Error("tickets: send ticket message", "err", err)
		}

		// Mention the role
		sess.ChannelMessageSend(channel.ID, fmt.Sprintf("<@&%s> - New ticket from <@%s>", notifyRoleID, creator.User.ID))

		slog.Info("tickets: created ticket", "channel_id", channel.ID, "guild_id", i.GuildID, "creator_id", creator.User.ID, "category", category)
		respondEdit(sess, i, fmt.Sprintf("✅ Ticket created: <#%s>", channel.ID))
	}
}

// formatCategory converts ticket category to display name
func formatCategory(category string) string {
	switch category {
	case tickets.CategoryASEPVE:
		return "ASE PVE"
	case tickets.CategoryASEPVP:
		return "ASE PVP"
	case tickets.CategoryMC:
		return "Minecraft"
	default:
		return strings.ToUpper(category)
	}
}
