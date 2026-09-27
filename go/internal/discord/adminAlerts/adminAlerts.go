// Package adminAlerts lets admins choose the server-wide channel where the
// bot reports problems that need an admin (see internal/alerts, which every
// feature uses to post there via bot.Alerts).
//
// Wiring: Register(bot) attaches the /adminalerts command from ./commands.
package adminAlerts

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/adminAlerts/commands"
)

// Register wires the admin alerts command into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// /adminalerts set|clear (Administrator by default)
	bot.AddCommand(commands.AdminAlertsCommand())
	bot.Router.SlashCommand("/adminalerts/set", commands.HandleSet(bot.Alerts))
	bot.Router.SlashCommand("/adminalerts/clear", commands.HandleClear(bot.Alerts))
}
