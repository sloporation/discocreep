// Package avc (auto voice channel) creates a personal voice channel for any
// user who joins a monitored channel, and deletes it once empty.
//
// Wiring lives here: Register(bot) attaches the voice-state event handlers
// from ./events and the /avc slash command from ./commands to the shared
// discord.Bot. Implementation lives in the subpackages.
package avc

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/events"
)

// Register wires the AVC feature into the bot. Call this before bot.Run.
func Register(bot *discord.Bot) {
	// Voice join: create a temp channel when a user enters a monitored hub.
	bot.AddHandler(events.HandleVoiceJoin(bot.DB))

	// Voice leave: delete the temp channel once its last member leaves.
	bot.AddHandler(events.HandleVoiceLeave(bot.DB))

	// /avc watch and /avc unwatch slash commands (Manage Channels required).
	bot.AddCommand(commands.AVCCommand(), commands.HandleAVC(bot.DB))
}
