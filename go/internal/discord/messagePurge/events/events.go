// Start a purge when a member leaves (if on-leave is on).
package events

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/shared"
)

// HandleMemberLeave returns a listener that queues a purge of the member's
// messages when they leave (or are kicked or banned), if the guild has
// turned on purge on leave.
func HandleMemberLeave(db *database.DB, purger *shared.Purger) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberLeave) {
		ctx := context.Background()
		settings, err := shared.LoadSettings(ctx, db, e.GuildID)
		if err != nil {
			slog.Error("messagePurge: load settings", "guild_id", e.GuildID, "err", err)
			return
		}
		if !settings.OnLeave {
			return
		}

		id, existing, err := purger.Queue(ctx, e.Client(), e.GuildID, e.User.ID, "leave", nil)
		if err != nil {
			slog.Error("messagePurge: queue purge on leave", "guild_id", e.GuildID, "user_id", e.User.ID, "err", err)
			return
		}
		slog.Info("messagePurge: purge queued on leave", "guild_id", e.GuildID, "user_id", e.User.ID, "id", id, "already_queued", existing)
	})
}
