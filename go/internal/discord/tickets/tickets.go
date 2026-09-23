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
)

const (
	// ChannelNameFormat is the format for ticket channel names
	// Format: ticket-{number}-{category}
	ChannelNameFormat = "ticket-{number}-{category}"

	// Component IDs
	CreateTicketButtonID = "ticket_create"
	CloseTicketButtonID  = "ticket_close"
	TicketModalID        = "ticket_modal"
	CategorySelectID     = "ticket_category_select"
	DescriptionInputID   = "ticket_description_input"
)

// Ticket categories
const (
	CategoryASEPVE = "ase-pve"
	CategoryASEPVP = "ase-pvp"
	CategoryMC     = "mc"
)

var Categories = []string{
	CategoryASEPVE,
	CategoryASEPVP,
	CategoryMC,
}

// Register wires the ticket system into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot, db *database.DB) {
	// /ticket setup command
	bot.AddCommand(commands.TicketCommand(), commands.HandleTicket(bot.Session, db))

	// Create ticket button
	bot.AddComponent(CreateTicketButtonID, components.HandleCreateTicketButton(db))

	// Modal submission
	bot.AddComponent(TicketModalID, components.HandleTicketModal(bot.Session, db))

	// Close ticket button
	bot.AddComponent(CloseTicketButtonID, components.HandleCloseTicket(bot.Session, db))
}
