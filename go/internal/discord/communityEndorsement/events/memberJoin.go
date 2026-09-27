// When a member joins a guild with community endorsement enabled, post a
// sponsorship request in the configured channel.
package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/shared"
)

// HandleMemberJoin returns a GuildMemberJoin listener that records a pending
// endorsement and posts it: a message in a text channel, or a new thread
// (with the optional tag) in a forum. Problems an admin must fix are sent to
// the admin alerts channel.
func HandleMemberJoin(db *database.DB, alerter *alerts.Alerter) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildMemberJoin) {
		user := e.Member.User
		if user.Bot {
			return
		}
		ctx := context.Background()

		var channelID, roleID snowflake.ID
		var tagID *snowflake.ID
		err := db.QueryRowContext(ctx,
			"SELECT channel_id, member_role_id, forum_tag_id FROM endorsement_configs WHERE guild_id = ? AND enabled",
			e.GuildID,
		).Scan(&channelID, &roleID, &tagID)
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

		postChannelID, messageID, err := post(e.Client(), alerter, e.GuildID, channelID, tagID, endorsementID, user)
		if err != nil {
			slog.Error("communityEndorsement: post sponsorship request", "guild_id", e.GuildID, "channel_id", channelID, "user_id", user.ID, "err", err)
			alerter.Send(e.Client(), e.GuildID, alerts.Alert{
				Feature: shared.Feature,
				Title:   "Couldn't post a sponsorship request",
				Description: fmt.Sprintf(
					"<@%s> (%s) joined, but the sponsorship request couldn't be posted in <#%s>, so nobody can sponsor them.\n\n"+
						"Give them <@&%s> by hand if they should be let in, then fix the channel or the bot's permissions there, or run `/endorsement setup` again.",
					user.ID, user.Username, channelID, roleID,
				),
				Fields: []discord.EmbedField{alerts.ErrorField(err)},
			})
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
// channel (for a forum, the new thread) and the message ID. A configured
// forum tag that no longer exists is skipped (with an admin alert) rather
// than failing the post.
func post(client *bot.Client, alerter *alerts.Alerter, guildID, channelID snowflake.ID, tagID *snowflake.ID, endorsementID int64, user discord.User) (snowflake.ID, snowflake.ID, error) {
	ch, err := shared.GuildChannel(client, channelID)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch channel: %w", err)
	}
	msg := shared.PendingPost(endorsementID, user)

	forum, isForum := ch.(discord.GuildForumChannel)
	if !isForum {
		m, err := client.Rest.CreateMessage(channelID, msg)
		if err != nil {
			return 0, 0, err
		}
		return channelID, m.ID, nil
	}

	thread := discord.ThreadChannelPostCreate{
		Name:    shared.ThreadName(user),
		Message: msg,
		// Archived threads can't be edited, so keep it open as long as possible.
		AutoArchiveDuration: discord.AutoArchiveDuration1w,
	}
	if tagID != nil {
		if hasTag(forum.AvailableTags, *tagID) {
			thread.AppliedTags = []snowflake.ID{*tagID}
		} else {
			alerter.Send(client, guildID, alerts.Alert{
				Feature: shared.Feature,
				Title:   "Sponsorship tag no longer exists",
				Description: fmt.Sprintf(
					"The tag configured for new joiner threads in <#%s> was deleted, so <@%s>'s thread was posted without it.\n\n"+
						"Run `/endorsement setup` again to pick a tag (or leave it out).",
					channelID, user.ID,
				),
			})
		}
	}

	p, err := client.Rest.CreatePostInThreadChannel(channelID, thread)
	if err != nil {
		return 0, 0, err
	}
	return p.ID(), p.Message.ID, nil
}

// hasTag reports whether tagID is one of the forum's tags.
func hasTag(tags []discord.ChannelTag, tagID snowflake.ID) bool {
	for _, t := range tags {
		if t.ID == tagID {
			return true
		}
	}
	return false
}
