// Package tickets provides a Discord ticket support system.
//
// Features:
//   - /ticket setup command to configure support channel, archive category, and notification role
//   - Modal-based ticket creation with category dropdown and description
//   - Automatic channel creation with proper permissions (creator + support role)
//   - Close button to archive tickets and remove send permissions
//   - Per-guild ticket number tracking
//
// Wiring: Register(bot) attaches slash commands and component handlers to the bot.
package tickets

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/components"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets/shared"
)

// Register wires the ticket system into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// /ticket setup command
	bot.AddCommand(commands.TicketCommand())
	bot.Router.SlashCommand("/ticket/setup", commands.HandleSetup(bot.DB))

	// Create ticket button
	bot.Router.ButtonComponent(shared.CreateTicketButtonID, components.HandleCreateTicketButton())

	// Modal submission
	bot.Router.Modal(shared.TicketModalID, components.HandleTicketModal(bot.DB))

	// Close ticket button
	bot.Router.ButtonComponent(shared.CloseTicketButtonID, components.HandleCloseTicket(bot.DB))
}
