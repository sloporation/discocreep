// Package permissionsync provides a command to copy permissions from one channel to another.
//
// The /copypermissions command allows admins to select a source channel and a
// destination channel, then overwrites the destination's permissions to match
// the source exactly.
//
// Wiring: Register(bot) attaches the slash command and its handler to the shared
// discord.Bot.
package permissionsync

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/permissionsync/commands"
)

// Register wires the permissionsync feature into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// /copypermissions command
	bot.AddCommand(commands.CopyPermissionsCommand(), commands.HandleCopyPermissions(bot.Session))
}
