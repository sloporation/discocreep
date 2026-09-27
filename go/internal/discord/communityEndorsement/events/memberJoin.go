// When a member joins a guild with community endorsement enabled, post a
// sponsorship request in the configured channel.
package events

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/shared"
)

// HandleMemberJoin returns a GuildMemberJoin listener that records a pending
// endorsement and posts it: a message in a text channel, or a new thread
// (with the optional tag) in a forum.
func HandleMemberJoin(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberJoin) {
		user := e.Member.User
		if user.Bot {
			return
		}
		ctx := context.Background()

		var channelID snowflake.ID
		var tagID *snowflake.ID
		err := db.QueryRowContext(ctx,
			"SELECT channel_id, forum_tag_id FROM endorsement_configs WHERE guild_id = ? AND enabled",
			e.GuildID,
		).Scan(&channelID, &tagID)
		if errors.Is(err, sql.ErrNoRows) {
			return // not enabled
		}
		if err != nil {
			slog.Error("communityEndorsement: load config", "guild_id", e.GuildID, "err", err)
			return
		}

		// Insert first: the button's custom_id carries the row ID.
		res, err := db.ExecContext(ctx,
			"INSERT INTO endorsements (guild_id, user_id) VALUES (?, ?)",
			e.GuildID, user.ID,
		)
		if err != nil {
			slog.Error("communityEndorsement: insert endorsement", "guild_id", e.GuildID, "user_id", user.ID, "err", err)
			return
		}
		endorsementID, err := res.LastInsertId()
		if err != nil {
			slog.Error("communityEndorsement: endorsement id", "err", err)
			return
		}

		postChannelID, messageID, err := post(e.Client(), channelID, tagID, endorsementID, user)
		if err != nil {
			slog.Error("communityEndorsement: post sponsorship request", "guild_id", e.GuildID, "channel_id", channelID, "user_id", user.ID, "err", err)
			// Nobody can sponsor a post that doesn't exist; drop the row.
			if _, err := db.ExecContext(ctx, "DELETE FROM endorsements WHERE id = ?", endorsementID); err != nil {
				slog.Error("communityEndorsement: delete unposted endorsement", "id", endorsementID, "err", err)
			}
			return
		}

		if _, err := db.ExecContext(ctx,
			"UPDATE endorsements SET channel_id = ?, message_id = ? WHERE id = ?",
			postChannelID, messageID, endorsementID,
		); err != nil {
			slog.Error("communityEndorsement: save post location", "id", endorsementID, "err", err)
		}

		slog.Info("communityEndorsement: posted sponsorship request", "guild_id", e.GuildID, "user_id", user.ID, "id", endorsementID)
	})
}

// post creates the sponsorship request and returns where it landed: the
// channel (for a forum, the new thread) and the message ID.
func post(client *bot.Client, channelID snowflake.ID, tagID *snowflake.ID, endorsementID int64, user discord.User) (snowflake.ID, snowflake.ID, error) {
	msg := shared.PendingPost(endorsementID, user)

	if isForum(client, channelID) {
		thread := discord.ThreadChannelPostCreate{
			Name:    shared.ThreadName(user),
			Message: msg,
			// Archived threads can't be edited, so keep it open as long as possible.
			AutoArchiveDuration: discord.AutoArchiveDuration1w,
		}
		if tagID != nil {
			thread.AppliedTags = []snowflake.ID{*tagID}
		}
		p, err := client.Rest.CreatePostInThreadChannel(channelID, thread)
		if err != nil {
			return 0, 0, err
		}
		return p.ID(), p.Message.ID, nil
	}

	m, err := client.Rest.CreateMessage(channelID, msg)
	if err != nil {
		return 0, 0, err
	}
	return channelID, m.ID, nil
}

// isForum reports whether channelID is a forum channel, from cache or REST.
func isForum(client *bot.Client, channelID snowflake.ID) bool {
	if ch, ok := client.Caches.Channel(channelID); ok {
		return ch.Type() == discord.ChannelTypeGuildForum
	}
	ch, err := client.Rest.GetChannel(channelID)
	return err == nil && ch.Type() == discord.ChannelTypeGuildForum
}
