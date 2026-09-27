// Package avc (auto voice channel) creates a personal voice channel for any
// user who joins a monitored channel, and deletes it once empty.
//
// Wiring lives here: Register(bot) attaches the voice-state event listeners
// from ./events, the /avc slash command from ./commands and the owner control
// panel buttons from ./components to the shared discord.Bot. Implementation
// lives in the subpackages.
package avc

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/components"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/events"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/shared"
)

// Register wires the AVC feature into the bot. Call this before bot.Run.
func Register(bot *discord.Bot) {
	// Voice join: create a temp channel when a user enters a monitored hub.
	bot.AddListener(events.HandleVoiceJoin(bot.DB, bot.Alerts))

	// Voice leave: delete the temp channel once its last member leaves.
	bot.AddListener(events.HandleVoiceLeave(bot.DB, bot.Alerts))

	// /avc watch and /avc unwatch slash commands (Manage Channels required).
	bot.AddCommand(commands.AVCCommand())
	bot.Router.SlashCommand("/avc/watch", commands.HandleWatch(bot.DB))
	bot.Router.SlashCommand("/avc/unwatch", commands.HandleUnwatch(bot.DB))

	// Owner control panel posted in each created channel's text chat.
	bot.Router.ButtonComponent(shared.HideButtonID, components.HandleHide(bot.DB, bot.Alerts))
	bot.Router.ButtonComponent(shared.UnhideButtonID, components.HandleUnhide(bot.DB, bot.Alerts))
	bot.Router.ButtonComponent(shared.RenameButtonID, components.HandleRenameButton(bot.DB))
	bot.Router.Modal(shared.RenameModalID, components.HandleRenameModal(bot.DB, bot.Alerts))
}
