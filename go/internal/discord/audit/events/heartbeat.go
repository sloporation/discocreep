// Keep audit_guild_state.last_seen_at fresh while the bot is connected, so
// after downtime it marks roughly when the bot went offline.
package events

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

const heartbeatInterval = time.Minute

// HandleReady returns a listener that starts the heartbeat the first time
// the bot connects. The heartbeat runs for the life of the process but skips
// ticks while the gateway is disconnected, so an outage isn't papered over.
func HandleReady(db *database.DB) bot.EventListener {
	var once sync.Once
	return bot.NewListenerFunc(func(e *events.Ready) {
		once.Do(func() {
			client := e.Client()
			go func() {
				ticker := time.NewTicker(heartbeatInterval)
				defer ticker.Stop()
				for range ticker.C {
					if client.Gateway == nil || client.Gateway.Status() != gateway.StatusReady {
						continue
					}
					heartbeat(client, db)
				}
			}()
		})
	})
}

// heartbeat refreshes last_seen_at for every guild the bot is in. Only rows
// created by a member sync are updated, so a guild whose sync failed is
// still treated as never synced.
func heartbeat(client *bot.Client, db *database.DB) {
	ctx := context.Background()
	for guild := range client.Caches.Guilds() {
		if _, err := db.ExecContext(ctx,
			"UPDATE audit_guild_state SET last_seen_at = NOW() WHERE guild_id = ?",
			guild.ID,
		); err != nil {
			slog.Error("audit: heartbeat", "guild_id", guild.ID, "err", err)
		}
	}
}
