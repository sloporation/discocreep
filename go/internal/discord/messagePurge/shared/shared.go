// Package shared holds what more than one messagePurge subpackage needs: the
// purge job runner (started by ./events on leave and by ./components on an
// admin's confirmation), the confirm button IDs, and the enabled/admin
// checks. It must not import any other messagePurge package, so that
// messagePurge and its subpackages can all depend on it without an import
// cycle.
package shared

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// Feature names this feature in admin alerts.
const Feature = "messagePurge"

// Routed component IDs. {user} is the user whose messages will be purged.
const (
	ConfirmButtonRoute = "/purge/confirm/{user}"
	CancelButtonID     = "/purge/cancel"
)

// ConfirmButtonID returns the custom_id of the confirm button for a user.
func ConfirmButtonID(userID snowflake.ID) string {
	return fmt.Sprintf("/purge/confirm/%s", userID)
}

// Enabled reports whether message purge is enabled for the guild.
func Enabled(ctx context.Context, db *database.DB, guildID snowflake.ID) (bool, error) {
	var enabled bool
	err := db.QueryRowContext(ctx,
		"SELECT enabled FROM purge_configs WHERE guild_id = ?",
		guildID,
	).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// IsAdmin reports whether the interacting member has Administrator. The
// commands are also Administrator-only by default, but a server can
// override default command permissions, so handlers check this too.
func IsAdmin(member *discord.ResolvedMember) bool {
	return member != nil && member.Permissions.Has(discord.PermissionAdministrator)
}

// ConfirmPrompt is the ephemeral "are you sure?" shown before an admin purge.
func ConfirmPrompt(user discord.User) discord.MessageCreate {
	return discord.MessageCreate{
		Embeds: []discord.Embed{{
			Color: 0xED4245,
			Title: "Delete all of this user's messages?",
			Description: fmt.Sprintf(
				"This deletes **every message** <@%s> (%s) has posted in this server, in every channel the bot can see. It can't be undone.\n\n"+
					"Large histories take a while: messages older than 14 days are deleted one at a time. A summary is posted to the admin alerts channel when it finishes.\n\n"+
					"The audit log keeps its copy of what was posted.",
				user.ID, user.Username,
			),
		}},
		Components: []discord.LayoutComponent{
			discord.NewActionRow(
				discord.NewDangerButton("Delete messages", ConfirmButtonID(user.ID)),
				discord.NewSecondaryButton("Cancel", CancelButtonID),
			),
		},
		Flags: discord.MessageFlagEphemeral,
	}
}
