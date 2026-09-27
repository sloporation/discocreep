package shared

import "github.com/disgoorg/disgo/discord"

// Snapshot converts the guild invites returned by Discord into cache entries
// keyed by invite code.
func Snapshot(invites []discord.ExtendedInvite) map[string]Invite {
	out := make(map[string]Invite, len(invites))
	for _, inv := range invites {
		entry := Invite{Uses: inv.Uses}
		if inv.Inviter != nil {
			id, name := inv.Inviter.ID, inv.Inviter.Username
			entry.InviterID, entry.InviterName = &id, &name
		}
		out[inv.Code] = entry
	}
	return out
}
