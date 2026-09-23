// Package loginLogger tracks member joins and leaves, sending notifications to configured channels.
//
// Features:
//   - Sends user join/leave messages to configured channels
//   - Admin join messages include the invite code and inviter
//   - Tracks invitation codes in-memory to match joins with invites
//
// Wiring: Register(bot) attaches event handlers from ./events and slash
// commands from ./commands to the shared discord.Bot.
package loginLogger

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/events"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/shared"
)

// Register wires the login logger feature into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// Shared between the Ready handler (seeds it) and the join handler (diffs it).
	inviteCache := shared.NewInviteCache()

	// Member join: track invite and send notifications
	bot.AddHandler(events.HandleMemberAdd(bot.DB, inviteCache))

	// Member leave: send notifications
	bot.AddHandler(events.HandleMemberRemove(bot.DB))

	// Ready: initialize invite cache
	bot.AddHandler(events.HandleReady(bot.Session, inviteCache))

	// /jll command with subcommands
	bot.AddCommand(commands.JLLCommand(), commands.HandleJLL(bot.DB))
}
