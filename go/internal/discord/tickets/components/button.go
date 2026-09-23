// Button handlers for ticket interactions.
package components

import (
	"context"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets"
)

// HandleCreateTicketButton returns the button handler for opening the ticket modal.
func HandleCreateTicketButton(db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// Show the ticket creation modal
		modal := &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: tickets.TicketModalID,
				Title:    "Create Support Ticket",
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.SelectMenu{
								CustomID:    tickets.CategorySelectID,
								Placeholder: "Select a category",
								MinValues:   1,
								MaxValues:   1,
								Options: []discordgo.SelectMenuOption{
									{
										Label: "ASE PVE",
										Value: tickets.CategoryASEPVE,
									},
									{
										Label: "ASE PVP",
										Value: tickets.CategoryASEPVP,
									},
									{
										Label: "Minecraft",
										Value: tickets.CategoryMC,
									},
								},
							},
						},
					},
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID:    tickets.DescriptionInputID,
								Label:       "Description",
								Style:       discordgo.TextInputParagraph,
								Placeholder: "Describe your issue...",
								Required:    true,
								MinLength:   10,
								MaxLength:   2000,
							},
						},
					},
				},
			},
		}

		if err := s.InteractionRespond(i.Interaction, modal); err != nil {
			slog.Error("tickets: show modal", "err", err)
		}
	}
}

// HandleCloseTicket returns the button handler for closing a ticket.
func HandleCloseTicket(s *discordgo.Session, db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(sess *discordgo.Session, i *discordgo.InteractionCreate) {
		// Defer the response
		if err := sess.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		}); err != nil {
			slog.Error("tickets: defer close interaction", "err", err)
			return
		}

		if err := closeTicket(sess, db, i.ChannelID, i.GuildID); err != nil {
			slog.Error("tickets: close ticket", "err", err)
			respondEdit(sess, i, "❌ Failed to close ticket.")
			return
		}

		respondEdit(sess, i, "✅ Ticket closed and archived.")
	}
}

// closeTicket closes a ticket and moves it to the archive category.
func closeTicket(s *discordgo.Session, db *database.DB, channelID, guildID string) error {
	ctx := context.Background()

	// Update ticket status in database
	query := "UPDATE tickets SET status = 'closed', closed_at = NOW() WHERE channel_id = ?"
	if _, err := db.ExecContext(ctx, query, channelID); err != nil {
		return err
	}

	// Get archive category from config
	var archiveCategoryID string
	err := db.QueryRowContext(ctx,
		"SELECT archive_category_id FROM ticket_configs WHERE guild_id = ?",
		guildID,
	).Scan(&archiveCategoryID)
	if err != nil {
		return err
	}

	// Remove send message permissions from @everyone
	if err := s.ChannelPermissionDelete(channelID, guildID); err != nil {
		slog.Warn("tickets: remove everyone permission", "err", err)
		// Non-fatal, continue
	}

	// Move channel to archive category
	if _, err := s.ChannelEditComplex(channelID, &discordgo.ChannelEdit{
		ParentID: archiveCategoryID,
	}); err != nil {
		return err
	}

	return nil
}

// respondEdit sends a deferred response message.
func respondEdit(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &msg,
	}); err != nil {
		slog.Error("tickets: respond edit", "err", err)
	}
}
