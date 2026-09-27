// Modal handler for ticket creation.
package components

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/shared"
)

// ticketPerms is what the creator and the notify role get on a ticket channel.
const ticketPerms = discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionReadMessageHistory

// HandleTicketModal returns the modal submission handler for ticket creation.
func HandleTicketModal(db *database.DB) handler.ModalHandler {
	return func(e *handler.ModalEvent) error {
		// Defer the response
		if err := e.DeferCreateMessage(false); err != nil {
			return fmt.Errorf("defer modal interaction: %w", err)
		}

		guildID := *e.GuildID()

		// Parse modal data
		var category string
		if values := e.Data.StringValues(shared.CategorySelectID); len(values) > 0 {
			category = values[0]
		}
		description := e.Data.Text(shared.DescriptionInputID)

		if category == "" || description == "" {
			return respondEdit(e, "❌ Please fill in all fields.")
		}

		// Get guild config
		var notifyRoleID, supportChannelID snowflake.ID
		if err := db.QueryRowContext(e.Ctx,
			"SELECT notify_role_id, support_channel_id FROM ticket_configs WHERE guild_id = ?",
			guildID,
		).Scan(&notifyRoleID, &supportChannelID); err != nil {
			slog.Error("tickets: fetch config", "err", err)
			return respondEdit(e, "❌ Ticket system not configured.")
		}

		// Get the support channel to determine the parent category
		var parentID snowflake.ID
		supportChannel, ok := e.Client().Caches.Channel(supportChannelID)
		if !ok {
			ch, err := e.Client().Rest.GetChannel(supportChannelID)
			if err != nil {
				slog.Error("tickets: fetch support channel", "err", err)
				return respondEdit(e, "❌ Could not fetch support channel.")
			}
			supportChannel, _ = ch.(discord.GuildChannel)
		}
		if supportChannel != nil && supportChannel.ParentID() != nil {
			parentID = *supportChannel.ParentID()
		}

		// Get next ticket number by counting tickets in this category
		var ticketNumber int
		if err := db.QueryRowContext(e.Ctx,
			"SELECT COUNT(*) + 1 FROM tickets WHERE guild_id = ? AND category = ?",
			guildID, category,
		).Scan(&ticketNumber); err != nil {
			ticketNumber = 1
		}

		creatorID := e.User().ID

		// Create the ticket channel with its permissions in one call, so it is
		// never visible to @everyone: creator + notify role can see and send
		// messages, @everyone (whose role ID is the guild ID) can't see it.
		channel, err := e.Client().Rest.CreateGuildChannel(guildID, discord.GuildTextChannelCreate{
			Name:     fmt.Sprintf("ticket-%d-%s", ticketNumber, category),
			ParentID: parentID, // Same parent as support channel
			PermissionOverwrites: []discord.PermissionOverwrite{
				discord.MemberPermissionOverwrite{UserID: creatorID, Allow: ticketPerms},
				discord.RolePermissionOverwrite{RoleID: notifyRoleID, Allow: ticketPerms},
				discord.RolePermissionOverwrite{RoleID: guildID, Deny: discord.PermissionViewChannel},
			},
		})
		if err != nil {
			slog.Error("tickets: create channel", "err", err)
			return respondEdit(e, "❌ Failed to create ticket channel.")
		}

		// Save ticket to database
		if _, err := db.ExecContext(e.Ctx,
			"INSERT INTO tickets (channel_id, guild_id, creator_id, category, description) VALUES (?, ?, ?, ?, ?)",
			channel.ID(), guildID, creatorID, category, description,
		); err != nil {
			slog.Error("tickets: save ticket", "err", err)
		}

		// Send initial message with close button, mentioning the support role
		if _, err := e.Client().Rest.CreateMessage(channel.ID(), discord.MessageCreate{
			Content: fmt.Sprintf("<@&%s> - New ticket from <@%s>", notifyRoleID, creatorID),
			Embeds: []discord.Embed{{
				Color:       0x5865F2,
				Title:       fmt.Sprintf("Ticket #%d - %s", ticketNumber, formatCategory(category)),
				Description: description,
				Fields: []discord.EmbedField{
					{Name: "Creator", Value: fmt.Sprintf("<@%s>", creatorID)},
					{Name: "Support Team", Value: fmt.Sprintf("<@&%s>", notifyRoleID)},
				},
			}},
			Components: []discord.LayoutComponent{
				discord.NewActionRow(
					discord.NewDangerButton("Close Ticket", shared.CloseTicketButtonID).
						WithEmoji(discord.NewComponentEmoji("🔒")),
				),
			},
		}); err != nil {
			slog.Error("tickets: send ticket message", "err", err)
		}

		slog.Info("tickets: created ticket", "channel_id", channel.ID(), "guild_id", guildID, "creator_id", creatorID, "category", category)
		return respondEdit(e, fmt.Sprintf("✅ Ticket created: <#%s>", channel.ID()))
	}
}

// formatCategory converts ticket category to display name
func formatCategory(category string) string {
	switch category {
	case shared.CategoryASEPVE:
		return "ASE PVE"
	case shared.CategoryASEPVP:
		return "ASE PVP"
	case shared.CategoryMC:
		return "Minecraft"
	default:
		return strings.ToUpper(category)
	}
}
