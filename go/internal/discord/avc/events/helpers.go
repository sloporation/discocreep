package events

import (
	"context"
	"log/slog"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// isMonitored reports whether channelID is a configured AVC hub for the given guild.
func isMonitored(db *database.DB, guildID, channelID string) bool {
	var count int
	err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM avc_monitors WHERE channel_id = ? AND guild_id = ?",
		channelID, guildID,
	).Scan(&count)
	return err == nil && count > 0
}

// isAVCChannel reports whether channelID is a bot-created temporary AVC channel.
func isAVCChannel(db *database.DB, channelID string) bool {
	var count int
	err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM avc_channels WHERE channel_id = ?",
		channelID,
	).Scan(&count)
	return err == nil && count > 0
}

// deleteAVCRecord removes a channel from avc_channels once it has been deleted
// or confirmed empty.
func deleteAVCRecord(db *database.DB, channelID string) {
	if _, err := db.ExecContext(context.Background(),
		"DELETE FROM avc_channels WHERE channel_id = ?",
		channelID,
	); err != nil {
		slog.Error("avc: remove avc_channels record", "channel_id", channelID, "err", err)
	}
}
