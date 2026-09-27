package events

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// feature names this feature in admin alerts.
const feature = "audit"

// How a join or leave was observed (audit_member_events.source).
const (
	sourceGateway     = "gateway"
	sourceBotOffline  = "bot_offline"
	sourceInitialSync = "initial_sync"
)

// recordJoin marks the user as in the guild and logs a join event.
func recordJoin(ctx context.Context, db *database.DB, guildID, userID snowflake.ID, username string, joinedAt time.Time, source string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_members (guild_id, user_id, username, first_joined_at, last_joined_at, in_guild)
		VALUES (?, ?, ?, ?, ?, TRUE)
		ON DUPLICATE KEY UPDATE
		username = VALUES(username),
		first_joined_at = COALESCE(first_joined_at, VALUES(first_joined_at)),
		last_joined_at = VALUES(last_joined_at),
		in_guild = TRUE`,
		guildID, userID, username, joinedAt, joinedAt,
	); err != nil {
		return fmt.Errorf("upsert member: %w", err)
	}
	if err := insertEvent(ctx, tx, guildID, userID, "join", source, joinedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// recordLeave marks the user as no longer in the guild and logs a leave
// event. username may be empty if it isn't known (the stored one is kept).
func recordLeave(ctx context.Context, db *database.DB, guildID, userID snowflake.ID, username string, leftAt time.Time, source string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// A member who joined before auditing started may have no row yet.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_members (guild_id, user_id, username, last_left_at, in_guild)
		VALUES (?, ?, ?, ?, FALSE)
		ON DUPLICATE KEY UPDATE
		username = IF(VALUES(username) = '', username, VALUES(username)),
		last_left_at = VALUES(last_left_at),
		in_guild = FALSE`,
		guildID, userID, username, leftAt,
	); err != nil {
		return fmt.Errorf("upsert member: %w", err)
	}
	if err := insertEvent(ctx, tx, guildID, userID, "leave", source, leftAt); err != nil {
		return err
	}
	return tx.Commit()
}

// insertEvent logs one join or leave in audit_member_events.
func insertEvent(ctx context.Context, tx *sql.Tx, guildID, userID snowflake.ID, event, source string, at time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO audit_member_events (guild_id, user_id, event, source, occurred_at) VALUES (?, ?, ?, ?, ?)",
		guildID, userID, event, source, at,
	); err != nil {
		return fmt.Errorf("insert %s event: %w", event, err)
	}
	return nil
}
