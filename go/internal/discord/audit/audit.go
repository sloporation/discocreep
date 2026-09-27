// Package audit keeps a record of every guild's members and activity.
//
// Features:
//   - Stores every message and every reaction added/removed
//   - Keeps a list of every user who has ever joined (audit_members) and a
//     separate log of each join and leave (audit_member_events)
//   - On connect, reconciles the stored member list with Discord's and
//     records anything missed while offline as a "bot_offline" event
//
// Needs the Server Members and Message Content privileged intents (set in
// discord.New); without Message Content, message content is stored empty.
//
// Wiring: Register(bot) attaches the event listeners from ./events.
package audit

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/audit/events"
)

// Register wires the audit feature into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// Member list: sync on connect / guild add, then track live joins and leaves
	bot.AddListener(events.HandleGuildReady(bot.DB, bot.Alerts))
	bot.AddListener(events.HandleGuildJoin(bot.DB, bot.Alerts))
	bot.AddListener(events.HandleMemberJoin(bot.DB))
	bot.AddListener(events.HandleMemberLeave(bot.DB))

	// Heartbeat: remember when the bot was last online
	bot.AddListener(events.HandleReady(bot.DB))

	// Activity
	bot.AddListener(events.HandleMessageCreate(bot.DB))
	bot.AddListener(events.HandleReactionAdd(bot.DB))
	bot.AddListener(events.HandleReactionRemove(bot.DB))
}
