// Package inviteTracker records which invite each member joined with and who
// created it, so admins can see who invited whom.
//
// Features:
//   - Snapshots every guild's invites on connect and diffs them on each join
//   - Stores one invite_joins row per join (rejoins included)
//   - /whoinvited <user> shows the result, visible only to the admin
//
// Wiring: Register(bot) attaches event listeners from ./events and slash
// commands from ./commands to the shared discord.Bot.
package inviteTracker

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/events"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/shared"
)

// Register wires the invite tracker into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// Shared between the GuildReady listener (seeds it) and the join listener (diffs it).
	inviteCache := shared.NewInviteCache()

	// Guild ready: snapshot invites
	bot.AddListener(events.HandleGuildReady(bot.Alerts, inviteCache))

	// Member join: work out and record the invite used
	bot.AddListener(events.HandleMemberJoin(bot.DB, inviteCache))

	// /whoinvited command (Administrator by default)
	bot.AddCommand(commands.WhoInvitedCommand())
	bot.Router.SlashCommand("/whoinvited", commands.HandleWhoInvited(bot.DB))
}
