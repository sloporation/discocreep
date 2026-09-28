package discord

import (
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/gateway"
)

// Intents are the gateway intents the watcher connects with. They live here
// so the watcher (which connects) and workers (whose caches must match what
// the watcher receives) agree.
//
// GuildMembers and MessageContent are privileged: "Server Members Intent"
// and "Message Content Intent" must be enabled in the Developer Portal, or
// the gateway closes with 4014 (Disallowed intents).
var Intents = []gateway.Intents{
	gateway.IntentGuilds,
	gateway.IntentGuildVoiceStates,
	gateway.IntentGuildMembers,
	gateway.IntentGuildMessages,
	gateway.IntentGuildMessageReactions,
	gateway.IntentMessageContent,
}

// CacheFlags are the disgo caches both processes keep. Voice states must be
// cached for disgo to report a member's previous voice state (avc); members
// for display names; channels and roles so permission and category lookups
// don't need a REST call. The watcher also needs them to build resync
// snapshots.
var CacheFlags = []cache.Flags{
	cache.FlagGuilds,
	cache.FlagChannels,
	cache.FlagRoles,
	cache.FlagMembers,
	cache.FlagVoiceStates,
}
