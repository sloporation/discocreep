// Log every reaction added to or removed from a guild message.
package events

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleReactionAdd returns a listener that records each reaction added.
func HandleReactionAdd(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMessageReactionAdd) {
		recordReaction(db, e.GenericGuildMessageReaction, "add")
	})
}

// HandleReactionRemove returns a listener that records each reaction removed.
func HandleReactionRemove(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMessageReactionRemove) {
		recordReaction(db, e.GenericGuildMessageReaction, "remove")
	})
}

func recordReaction(db *database.DB, e *events.GenericGuildMessageReaction, action string) {
	// Name is the unicode emoji, or the custom emoji's name (nil if the
	// custom emoji has since been deleted).
	emoji := "unknown"
	if e.Emoji.Name != nil {
		emoji = *e.Emoji.Name
	}

	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO audit_reactions (guild_id, channel_id, message_id, user_id, emoji, emoji_id, action)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.GuildID, e.ChannelID, e.MessageID, e.UserID, emoji, e.Emoji.ID, action,
	); err != nil {
		slog.Error("audit: record reaction", "guild_id", e.GuildID, "message_id", e.MessageID, "err", err)
	}
}
