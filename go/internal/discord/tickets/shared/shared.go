// Package shared holds constants used by more than one tickets subpackage
// (component custom_ids, ticket categories). It must not import any other
// tickets package, so that tickets, ./commands and ./components can all
// depend on it without an import cycle.
package shared

const (
	// ChannelNameFormat is the format for ticket channel names
	// Format: ticket-{number}-{category}
	ChannelNameFormat = "ticket-{number}-{category}"

	// Routed component IDs. The interaction router only dispatches custom_ids
	// that start with "/".
	CreateTicketButtonID = "/ticket/create"
	CloseTicketButtonID  = "/ticket/close"
	TicketModalID        = "/ticket/modal"

	// Modal field IDs; read from the modal submission, not routed.
	CategorySelectID   = "ticket_category_select"
	DescriptionInputID = "ticket_description_input"
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
