// Package loginLogger tracks member joins and leaves, sending notifications to configured channels.
//
// Features:
//   - Sends user join/leave messages to configured channels
//   - Admin join messages include the invite code and inviter
//   - Tracks invitation codes in-memory to match joins with invites
//
// Wiring: Register(bot, db) attaches event handlers from ./events and slash
// commands from ./commands to the shared discord.Bot.
package loginLogger

import (
	"sync"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger/events"
)

// InviteCache stores a snapshot of invites per guild to track which invite was used.
// Key: guildID, Value: map[inviteCode]usageCount
type InviteCache struct {
	mu    sync.RWMutex
	cache map[string]map[string]int
}

var inviteCache = &InviteCache{
	cache: make(map[string]map[string]int),
}

// SetInvites stores the current invite usage counts for a guild.
func (ic *InviteCache) SetInvites(guildID string, invites map[string]int) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.cache[guildID] = invites
}

// GetInvites retrieves the cached invites for a guild.
func (ic *InviteCache) GetInvites(guildID string) map[string]int {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	invites, ok := ic.cache[guildID]
	if !ok {
		return make(map[string]int)
	}
	return invites
}

// Register wires the login logger feature into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	// Member join: track invite and send notifications
	bot.AddHandler(events.HandleMemberAdd(bot.DB, inviteCache))

	// Member leave: send notifications
	bot.AddHandler(events.HandleMemberRemove(bot.DB))

	// Ready: initialize invite cache
	bot.AddHandler(events.HandleReady(bot.Session, inviteCache))

	// /jll command with subcommands
	bot.AddCommand(commands.JLLCommand(), commands.HandleJLL(bot.DB))
}
