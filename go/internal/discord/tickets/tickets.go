// Package tickets provides a Discord ticket support system.
//
// Features:
//   - /ticket setup command to configure support channel, archive category, and notification role
//   - Modal-based ticket creation with category dropdown and description
//   - Automatic channel creation with proper permissions (creator + support role)
//   - Close button to archive tickets and remove send permissions
//   - Per-guild ticket number tracking
//
// Wiring: Register(bot, db) attaches slash commands and component handlers to the bot.
package tickets

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/components"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/shared"
)

// Register wires the ticket system into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot, db *database.DB) {
	// /ticket setup command
	bot.AddCommand(commands.TicketCommand(), commands.HandleTicket(bot.Session, db))

	// Create ticket button
	bot.AddComponent(shared.CreateTicketButtonID, components.HandleCreateTicketButton(db))

	// Modal submission
	bot.AddComponent(shared.TicketModalID, components.HandleTicketModal(bot.Session, db))

	// Close ticket button
	bot.AddComponent(shared.CloseTicketButtonID, components.HandleCloseTicket(bot.Session, db))
}
