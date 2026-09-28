// Package messagePurge deletes a user's messages from Discord.
//
// Features, each switched on separately with /purge settings (both off by
// default):
//   - on-leave: when a member leaves (or is kicked/banned), every message
//     they posted in the server is deleted
//   - admin-purge: /purge user lets an administrator delete a user's
//     messages on demand, after a confirmation
//   - Purges run in the background on exactly one worker, survive restarts
//     and worker crashes, and post a summary to the admin alerts channel
//
// Messages are found with Discord's guild message search, so this covers
// history from before the bot joined. Messages younger than 14 days are
// bulk-deleted; older ones must be deleted one at a time, so large histories
// take a while.
//
// This only deletes from Discord. The audit log (audit_messages) is never
// touched: it's an audit log and keeps what was posted.
//
// Wiring: Register(bot) attaches the listeners from ./events, the /purge
// command from ./commands and the confirm buttons from ./components.
package messagePurge

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/components"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/events"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/shared"
)

// Register wires message purge into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// Runs purge jobs in the background; shared by the leave listener and the confirm button.
	purger := shared.NewPurger(bot.DB, bot.Alerts, bot.Locks)

	// Member leave: purge their messages (if on-leave is on)
	bot.AddListener(events.HandleMemberLeave(bot.DB, purger))

	// Resume purges interrupted by a restart or a worker dying
	bot.AddStartHook(purger.ResumeLoop(bot.Client))

	// /purge settings|user (Administrator, enforced in handlers)
	bot.AddCommand(commands.PurgeCommand())
	bot.Router.SlashCommand("/purge/settings", commands.HandleSettings(bot.DB))
	bot.Router.SlashCommand("/purge/user", commands.HandleUser(bot.DB))

	// Confirmation prompt buttons for /purge user
	bot.Router.ButtonComponent(shared.ConfirmButtonRoute, components.HandleConfirm(bot.DB, purger))
	bot.Router.ButtonComponent(shared.CancelButtonID, components.HandleCancel())
}
