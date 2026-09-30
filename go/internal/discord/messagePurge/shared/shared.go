// Package shared holds what more than one messagePurge subpackage needs: the
// purge job runner (started by ./events on leave and by ./components on an
// admin's confirmation), the confirm button IDs, and the settings/admin
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

// Settings are a guild's message purge switches. Both are off by default
// and independent of each other.
type Settings struct {
	// OnLeave deletes a member's messages when they leave.
	OnLeave bool
	// AdminPurge allows administrators to use /purge user.
	AdminPurge bool
}

// LoadSettings returns the guild's message purge settings.
func LoadSettings(ctx context.Context, db *database.DB, guildID snowflake.ID) (Settings, error) {
	var s Settings
	err := db.QueryRowContext(ctx,
		"SELECT on_leave, admin_purge FROM purge_configs WHERE guild_id = ?",
		guildID,
	).Scan(&s.OnLeave, &s.AdminPurge)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, nil
	}
	return s, err
}

// SaveSettings stores the guild's message purge settings. Used by /purge
// settings and the web API.
func SaveSettings(ctx context.Context, db *database.DB, guildID snowflake.ID, s Settings) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO purge_configs (guild_id, on_leave, admin_purge) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE on_leave = VALUES(on_leave), admin_purge = VALUES(admin_purge)`,
		guildID, s.OnLeave, s.AdminPurge,
	)
	return err
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
