// Log every message posted in a guild.
package events

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleMessageCreate returns a listener that stores each guild message.
// Content is only present if the Message Content intent is enabled.
func HandleMessageCreate(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMessageCreate) {
		msg := e.Message

		var attachments *string
		if len(msg.Attachments) > 0 {
			urls := make([]string, len(msg.Attachments))
			for i, a := range msg.Attachments {
				urls[i] = a.URL
			}
			b, _ := json.Marshal(urls)
			s := string(b)
			attachments = &s
		}

		// IGNORE: a replayed event after a gateway resume is a duplicate.
		if _, err := db.ExecContext(context.Background(), `
			INSERT IGNORE INTO audit_messages
			(message_id, guild_id, channel_id, author_id, author_bot, content, attachments, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.ID, e.GuildID, e.ChannelID, msg.Author.ID, msg.Author.Bot, msg.Content, attachments, msg.CreatedAt,
		); err != nil {
			slog.Error("audit: record message", "guild_id", e.GuildID, "message_id", msg.ID, "err", err)
		}
	})
}
