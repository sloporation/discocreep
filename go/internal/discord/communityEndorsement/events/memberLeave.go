// When a member leaves before being sponsored, close their sponsorship post.
package events

import (
	"context"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/shared"
)

// HandleMemberLeave returns a GuildMemberLeave listener that marks the
// member's pending endorsements as left and removes the sponsor button.
func HandleMemberLeave(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberLeave) {
		ctx := context.Background()

		rows, err := db.QueryContext(ctx,
			"SELECT id, channel_id, message_id FROM endorsements WHERE guild_id = ? AND user_id = ? AND status = 'pending'",
			e.GuildID, e.User.ID,
		)
		if err != nil {
			slog.Error("communityEndorsement: find pending endorsements", "guild_id", e.GuildID, "user_id", e.User.ID, "err", err)
			return
		}
		type pending struct {
			id                   int64
			channelID, messageID *snowflake.ID
		}
		var found []pending
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.id, &p.channelID, &p.messageID); err != nil {
				slog.Error("communityEndorsement: scan endorsement", "err", err)
				continue
			}
			found = append(found, p)
		}
		rows.Close()

		client := e.Client()
		for _, p := range found {
			// Only close it if it's still pending (a sponsor may have just clicked).
			res, err := db.ExecContext(ctx,
				"UPDATE endorsements SET status = 'left' WHERE id = ? AND status = 'pending'",
				p.id,
			)
			if err != nil {
				slog.Error("communityEndorsement: mark left", "id", p.id, "err", err)
				continue
			}
			if n, _ := res.RowsAffected(); n == 0 || p.channelID == nil || p.messageID == nil {
				continue
			}

			msg, err := client.Rest.GetMessage(*p.channelID, *p.messageID)
			if err != nil {
				slog.Warn("communityEndorsement: fetch post", "id", p.id, "err", err)
				continue
			}
			var embed discord.Embed
			if len(msg.Embeds) > 0 {
				embed = msg.Embeds[0]
			}
			if _, err := client.Rest.UpdateMessage(*p.channelID, *p.messageID, shared.Left(embed)); err != nil {
				slog.Warn("communityEndorsement: update post", "id", p.id, "err", err)
			}
			slog.Info("communityEndorsement: joiner left before sponsorship", "guild_id", e.GuildID, "user_id", e.User.ID, "id", p.id)
		}
	})
}
