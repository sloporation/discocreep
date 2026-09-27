// Log member joins and leaves as they happen.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleMemberJoin returns a listener that records each join.
func HandleMemberJoin(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberJoin) {
		user := e.Member.User
		joinedAt := time.Now()
		if e.Member.JoinedAt != nil {
			joinedAt = *e.Member.JoinedAt
		}
		if err := recordJoin(context.Background(), db, e.GuildID, user.ID, user.Username, joinedAt, sourceGateway); err != nil {
			slog.Error("audit: record join", "guild_id", e.GuildID, "user_id", user.ID, "err", err)
		}
	})
}

// HandleMemberLeave returns a listener that records each leave (including
// kicks and bans, which Discord reports the same way).
func HandleMemberLeave(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberLeave) {
		if err := recordLeave(context.Background(), db, e.GuildID, e.User.ID, e.User.Username, time.Now(), sourceGateway); err != nil {
			slog.Error("audit: record leave", "guild_id", e.GuildID, "user_id", e.User.ID, "err", err)
		}
	})
}
