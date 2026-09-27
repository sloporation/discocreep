// Package communityEndorsement holds new joiners in "purgatory" until an
// existing member sponsors them.
//
// Features:
//   - /endorsement setup picks a text channel or forum, the role sponsoring
//     grants, and (forums only) a tag for new joiner threads
//   - Each join posts "do you want to sponsor this person?" with a button:
//     a message in a text channel, or a new thread in a forum
//   - The first member to press it grants the role and is recorded as having
//     let them in; the post is updated in place
//   - If the joiner leaves first, the post is marked as such
//
// The server must hide its channels from @everyone and grant access through
// the member role, otherwise new joiners aren't actually held back.
//
// Wiring: Register(bot) attaches event listeners from ./events, slash
// commands from ./commands and the sponsor button from ./components.
package communityEndorsement

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/components"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/events"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/shared"
)

// Register wires community endorsement into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// Member join: post a sponsorship request
	bot.AddListener(events.HandleMemberJoin(bot.DB))

	// Member leave: close their pending request
	bot.AddListener(events.HandleMemberLeave(bot.DB))

	// /endorsement setup|disable (Administrator by default)
	bot.AddCommand(commands.EndorsementCommand())
	bot.Router.SlashCommand("/endorsement/setup", commands.HandleSetup(bot.DB))
	bot.Router.SlashCommand("/endorsement/disable", commands.HandleDisable(bot.DB))
	bot.Router.Autocomplete("/endorsement/setup", commands.HandleSetupAutocomplete())

	// Sponsor button on each request
	bot.Router.ButtonComponent(shared.SponsorButtonRoute, components.HandleSponsor(bot.DB))
}
