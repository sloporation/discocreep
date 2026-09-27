// When a member joins, work out which invite they used and record it along
// with the invite's creator.
package events

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/shared"
)

// usedInvite is the invite a member joined with.
type usedInvite struct {
	Code string
	shared.Invite
}

// HandleMemberJoin returns a GuildMemberJoin listener that records the invite
// each new member used in invite_joins. A row is written even when the invite
// can't be determined (vanity URL, Server Discovery, ...), with NULL invite
// columns.
func HandleMemberJoin(db *database.DB, cache *shared.InviteCache) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberJoin) {
		// Bots are added via OAuth, not invites.
		if e.Member.User.Bot {
			return
		}

		invite := findUsedInvite(e.Client(), e.GuildID, cache)

		var code *string
		var inviterID *snowflake.ID
		var inviterName *string
		if invite != nil {
			code, inviterID, inviterName = &invite.Code, invite.InviterID, invite.InviterName
		}

		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO invite_joins (guild_id, user_id, invite_code, inviter_id, inviter_name) VALUES (?, ?, ?, ?, ?)",
			e.GuildID, e.Member.User.ID, code, inviterID, inviterName,
		); err != nil {
			slog.Error("inviteTracker: record join", "guild_id", e.GuildID, "user_id", e.Member.User.ID, "err", err)
			return
		}

		slog.Info("inviteTracker: recorded join", "guild_id", e.GuildID, "user_id", e.Member.User.ID, "invite", code, "inviter_id", inviterID)
	})
}

// findUsedInvite fetches the guild's current invites, swaps them into the
// cache, and returns the invite whose use count went up since the previous
// snapshot. Returns nil if it can't be determined.
func findUsedInvite(client *bot.Client, guildID snowflake.ID, cache *shared.InviteCache) *usedInvite {
	invites, err := client.Rest.GetGuildInvites(guildID)
	if err != nil {
		slog.Error("inviteTracker: fetch invites", "guild_id", guildID, "err", err)
		return nil
	}

	current := shared.Snapshot(invites)
	return diffInvites(cache.Swap(guildID, current), current)
}

// diffInvites returns the invite used between two snapshots, or nil if it
// can't be determined.
func diffInvites(old, current map[string]shared.Invite) *usedInvite {
	if old == nil {
		// No earlier snapshot (the GuildReady fetch failed), so there's
		// nothing to diff against. The cache is seeded for the next join.
		return nil
	}

	// Used if its count went up, or it's new since the last snapshot and
	// already has a use.
	for code, inv := range current {
		prev, existed := old[code]
		if (existed && inv.Uses > prev.Uses) || (!existed && inv.Uses > 0) {
			return &usedInvite{Code: code, Invite: inv}
		}
	}

	// Single-use (or max-uses) invites are deleted as soon as they're used,
	// so they vanish from the list instead of incrementing. Only trust this
	// when exactly one disappeared; an invite can also vanish by expiring.
	var gone *usedInvite
	for code, inv := range old {
		if _, ok := current[code]; ok {
			continue
		}
		if gone != nil {
			return nil
		}
		gone = &usedInvite{Code: code, Invite: inv}
	}
	return gone
}
