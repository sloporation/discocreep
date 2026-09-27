// Package shared holds types used by more than one inviteTracker package.
// It must not import any other inviteTracker package, so that inviteTracker
// and its subpackages can all depend on it without an import cycle.
package shared

import (
	"sync"

	"github.com/disgoorg/snowflake/v2"
)

// Invite is the cached state of one invite.
type Invite struct {
	Uses        int
	InviterID   *snowflake.ID
	InviterName *string
}

// InviteCache stores a snapshot of each guild's invites, so a join can be
// matched to the invite whose use count went up (or that disappeared).
// Key: guildID, Value: map[inviteCode]Invite
type InviteCache struct {
	mu    sync.Mutex
	cache map[snowflake.ID]map[string]Invite
}

// NewInviteCache returns an empty, ready-to-use InviteCache.
func NewInviteCache() *InviteCache {
	return &InviteCache{cache: make(map[snowflake.ID]map[string]Invite)}
}

// Set stores the current invites for a guild.
func (ic *InviteCache) Set(guildID snowflake.ID, invites map[string]Invite) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.cache[guildID] = invites
}

// Swap stores the new invites for a guild and returns the previous
// snapshot. Doing both under one lock means two joins processed at the same
// time can't both diff against the same old snapshot.
func (ic *InviteCache) Swap(guildID snowflake.ID, invites map[string]Invite) map[string]Invite {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	old := ic.cache[guildID]
	ic.cache[guildID] = invites
	return old
}
