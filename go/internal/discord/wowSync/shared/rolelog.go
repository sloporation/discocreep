package shared

import (
	"context"
	"strings"

	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// RoleAction is what the sync did with a member's role (wow_sync_role_log.action).
type RoleAction string

const (
	// RoleAdd: the bot gave the member the role.
	RoleAdd RoleAction = "add"
	// RoleRemove: the bot took the role away.
	RoleRemove RoleAction = "remove"
	// RoleAdopt: the member already had a role their rank grants; the bot
	// is now responsible for it (no change in Discord).
	RoleAdopt RoleAction = "adopt"
	// RoleRelease: the role was taken off outside the bot, or the member
	// left the server; the bot is no longer responsible for it (no change
	// in Discord).
	RoleRelease RoleAction = "release"
)

// maxReasonLength fits wow_sync_role_log.reason.
const maxReasonLength = 255

type roleLogEntry struct {
	userID, roleID snowflake.ID
	action         RoleAction
	reason         string
}

// HeldRoles returns, per member, the roles the bot is responsible for in a
// server: those whose latest log entry is an add or an adopt.
func HeldRoles(ctx context.Context, db *database.DB, guildID snowflake.ID) (map[snowflake.ID]map[snowflake.ID]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT l.discord_user_id, l.role_id FROM wow_sync_role_log l
		JOIN (
			SELECT MAX(id) AS id FROM wow_sync_role_log WHERE guild_id = ? GROUP BY discord_user_id, role_id
		) latest ON latest.id = l.id
		WHERE l.action IN ('add', 'adopt')`,
		guildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[snowflake.ID]map[snowflake.ID]bool{}
	for rows.Next() {
		var user, role snowflake.ID
		if err := rows.Scan(&user, &role); err != nil {
			return nil, err
		}
		if out[user] == nil {
			out[user] = map[snowflake.ID]bool{}
		}
		out[user][role] = true
	}
	return out, rows.Err()
}

// logRoles appends entries to the role log.
func logRoles(ctx context.Context, db *database.DB, guildID snowflake.ID, entries []roleLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	var sb strings.Builder
	args := make([]any, 0, len(entries)*5)
	sb.WriteString("INSERT INTO wow_sync_role_log (guild_id, discord_user_id, role_id, action, reason) VALUES ")
	for i, e := range entries {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?, ?, ?, ?)")
		reason := e.reason
		if len([]rune(reason)) > maxReasonLength {
			reason = string([]rune(reason)[:maxReasonLength])
		}
		args = append(args, guildID, e.userID, e.roleID, e.action, reason)
	}
	_, err := db.ExecContext(ctx, sb.String(), args...)
	return err
}
