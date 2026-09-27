// Button handlers for ticket interactions.
package components

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/shared"
)

// HandleCreateTicketButton returns the button handler for opening the ticket modal.
func HandleCreateTicketButton() handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		// Select menus in modals must be wrapped in a Label (not an action row).
		return e.Modal(discord.NewModalCreate(shared.TicketModalID, "Create Support Ticket",
			discord.NewLabel("Category",
				discord.NewStringSelectMenu(shared.CategorySelectID, "Select a category",
					discord.NewStringSelectMenuOption("ASE PVE", shared.CategoryASEPVE),
					discord.NewStringSelectMenuOption("ASE PVP", shared.CategoryASEPVP),
					discord.NewStringSelectMenuOption("Minecraft", shared.CategoryMC),
				).WithRequired(true),
			),
			discord.NewLabel("Description",
				discord.NewParagraphTextInput(shared.DescriptionInputID).
					WithPlaceholder("Describe your issue...").
					WithRequired(true).
					WithMinLength(10).
					WithMaxLength(2000),
			),
		))
	}
}

// HandleCloseTicket returns the button handler for closing a ticket.
func HandleCloseTicket(db *database.DB, alerter *alerts.Alerter) handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		// Defer the response
		if err := e.DeferCreateMessage(false); err != nil {
			return fmt.Errorf("defer close interaction: %w", err)
		}

		if err := closeTicket(e.Ctx, e.Client(), db, e.Channel().ID(), *e.GuildID()); err != nil {
			slog.Error("tickets: close ticket", "err", err)
			alerter.Send(e.Client(), *e.GuildID(), alerts.Alert{
				Feature:     shared.Feature,
				Title:       "Couldn't close a ticket",
				Description: fmt.Sprintf("<@%s> tried to close <#%s>, but it couldn't be archived. Check the archive category still exists (or run `/ticket setup` again) and the bot has Manage Channels.", e.User().ID, e.Channel().ID()),
				Fields:      []discord.EmbedField{alerts.ErrorField(err)},
			})
			return respondEdit(e, "❌ Failed to close ticket. The admins have been alerted.")
		}

		return respondEdit(e, "✅ Ticket closed and archived.")
	}
}

// closeTicket marks a ticket closed, revokes the creator's send permission and
// moves the channel to the archive category.
func closeTicket(ctx context.Context, client *bot.Client, db *database.DB, channelID, guildID snowflake.ID) error {
	// Update ticket status in database
	if _, err := db.ExecContext(ctx,
		"UPDATE tickets SET status = 'closed', closed_at = NOW() WHERE channel_id = ?",
		channelID,
	); err != nil {
		return err
	}

	var archiveCategoryID snowflake.ID
	if err := db.QueryRowContext(ctx,
		"SELECT archive_category_id FROM ticket_configs WHERE guild_id = ?",
		guildID,
	).Scan(&archiveCategoryID); err != nil {
		return err
	}

	// Creator keeps read access but can no longer send. @everyone's deny on
	// View Channel is left in place so the archived ticket stays private.
	var creatorID snowflake.ID
	if err := db.QueryRowContext(ctx,
		"SELECT creator_id FROM tickets WHERE channel_id = ?",
		channelID,
	).Scan(&creatorID); err != nil {
		slog.Warn("tickets: look up ticket creator", "channel_id", channelID, "err", err)
	} else {
		allow := discord.PermissionViewChannel | discord.PermissionReadMessageHistory
		deny := discord.PermissionSendMessages
		if err := client.Rest.UpdatePermissionOverwrite(channelID, creatorID, discord.MemberPermissionOverwriteUpdate{
			Allow: &allow,
			Deny:  &deny,
		}); err != nil {
			slog.Warn("tickets: revoke creator send permission", "err", err)
			// Non-fatal, continue
		}
	}

	// Move channel to archive category
	if _, err := client.Rest.UpdateChannel(channelID, discord.GuildTextChannelUpdate{
		ParentID: &archiveCategoryID,
	}); err != nil {
		return err
	}

	return nil
}

// responseEditor is satisfied by both *handler.ComponentEvent and *handler.ModalEvent.
type responseEditor interface {
	UpdateInteractionResponse(discord.MessageUpdate, ...rest.RequestOpt) (*discord.Message, error)
}

// respondEdit edits the deferred interaction response.
func respondEdit(e responseEditor, msg string) error {
	_, err := e.UpdateInteractionResponse(discord.MessageUpdate{Content: &msg})
	return err
}
