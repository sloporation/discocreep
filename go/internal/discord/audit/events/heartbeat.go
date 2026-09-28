// Keep audit_guild_state.last_seen_at fresh while the bot is connected, so
// after downtime it marks roughly when the bot went offline.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/bot"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

const heartbeatInterval = time.Minute

// GatewayStatus reports whether the bot's gateway connection (held by the
// watcher process) is up. Satisfied by bot.Gateway.
type GatewayStatus interface {
	Up(ctx context.Context) bool
}

// Heartbeat returns a start hook that refreshes last_seen_at every minute
// for the guilds this worker handles. Ticks are skipped while the gateway is
// down, so an outage isn't papered over.
func Heartbeat(client *bot.Client, db *database.DB, gateway GatewayStatus) func(ctx context.Context) {
	return func(ctx context.Context) {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !gateway.Up(ctx) {
				continue
			}
			heartbeat(ctx, client, db)
		}
	}
}

// heartbeat refreshes last_seen_at for every guild in this worker's cache
// (the guilds of the partitions it owns). Only rows created by a member sync
// are updated, so a guild whose sync failed is still treated as never synced.
func heartbeat(ctx context.Context, client *bot.Client, db *database.DB) {
	for guild := range client.Caches.Guilds() {
		if _, err := db.ExecContext(ctx,
			"UPDATE audit_guild_state SET last_seen_at = NOW() WHERE guild_id = ?",
			guild.ID,
		); err != nil {
			slog.Error("audit: heartbeat", "guild_id", guild.ID, "err", err)
		}
	}
}
