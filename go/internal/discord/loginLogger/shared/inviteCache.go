// Package shared holds types used by more than one loginLogger subpackage.
// It must not import any other loginLogger package, so that loginLogger,
// ./events and ./commands can all depend on it without an import cycle.
package shared

import (
	"sync"

	"github.com/disgoorg/snowflake/v2"
)

// InviteCache stores a snapshot of invites per guild to track which invite was used.
// Key: guildID, Value: map[inviteCode]usageCount
type InviteCache struct {
	mu    sync.RWMutex
	cache map[snowflake.ID]map[string]int
}

// NewInviteCache returns an empty, ready-to-use InviteCache.
func NewInviteCache() *InviteCache {
	return &InviteCache{cache: make(map[snowflake.ID]map[string]int)}
}

// SetInvites stores the current invite usage counts for a guild.
func (ic *InviteCache) SetInvites(guildID snowflake.ID, invites map[string]int) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.cache[guildID] = invites
}

// GetInvites retrieves the cached invites for a guild.
func (ic *InviteCache) GetInvites(guildID snowflake.ID) map[string]int {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	invites, ok := ic.cache[guildID]
	if !ok {
		return make(map[string]int)
	}
	return invites
}
