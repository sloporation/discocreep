// When a user joins a voice channel, and that voice channel is monitored for
// this guild, create a voice channel as per the configuration
// IE if the voice channel should be created underneath another channel, then
// create it there. Else if it should be created in a category then create it
// there. If no optional secondary channel/category provided, then create it
// directly underneath the watched channel.
package events

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/shared"
)

// HandleVoiceJoin returns a listener that creates a personal voice channel
// whenever a user joins (or is moved into) a monitored hub channel.
//
// Flow:
//  1. Ignore events that are not a join (ChannelID nil) or are joins into
//     an already-created AVC channel (prevents feedback loops).
//  2. Check the avc_monitors table — exit if the channel is not a hub.
//  3. Resolve the member's display name and the hub's parent category.
//  4. Create a new voice channel in that category (or top-level if none).
//  5. Record the new channel in avc_channels and move the user into it.
//  6. Post the owner's control panel (hide/unhide/rename) in its text chat.
func HandleVoiceJoin(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildVoiceStateUpdate) {
		vs := e.VoiceState

		// Not a join/move — user disconnected.
		if vs.ChannelID == nil {
			return
		}
		channelID := *vs.ChannelID

		// Channel didn't change (e.g. mute/deafen update).
		if e.OldVoiceState.ChannelID != nil && *e.OldVoiceState.ChannelID == channelID {
			return
		}

		// User joined one of our own temp channels — nothing to create.
		if isAVCChannel(db, channelID) {
			return
		}

		// Not a monitored hub.
		if !isMonitored(db, vs.GuildID, channelID) {
			return
		}

		client := e.Client()

		// Look up the hub channel so we know its parent category (may be nil).
		hub, ok := client.Caches.Channel(channelID)
		if !ok {
			ch, err := client.Rest.GetChannel(channelID)
			if err != nil {
				slog.Error("avc: fetch hub channel", "channel_id", channelID, "err", err)
				return
			}
			if hub, ok = ch.(discord.GuildChannel); !ok {
				slog.Error("avc: hub is not a guild channel", "channel_id", channelID)
				return
			}
		}

		// Create the temporary voice channel in the same category as the hub.
		// If the hub has no parent category, ParentID stays 0 (omitted) and
		// Discord places the channel at the top level, which is the intended
		// fallback.
		var parentID snowflake.ID
		if p := hub.ParentID(); p != nil {
			parentID = *p
		}
		created, err := client.Rest.CreateGuildChannel(vs.GuildID, discord.GuildVoiceChannelCreate{
			Name:     fmt.Sprintf("%s's Channel", e.Member.EffectiveName()),
			ParentID: parentID,
		})
		if err != nil {
			slog.Error("avc: create voice channel", "guild_id", vs.GuildID, "err", err)
			return
		}

		slog.Info("avc: created channel", "channel", created.Name(), "id", created.ID(), "guild_id", vs.GuildID)

		// Track the channel so the leave handler knows to clean it up.
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO avc_channels (channel_id, owner_id, guild_id) VALUES (?, ?, ?)",
			created.ID(), vs.UserID, vs.GuildID,
		); err != nil {
			slog.Error("avc: insert avc_channels", "channel_id", created.ID(), "err", err)
			// Non-fatal: still move the user; worst case the channel leaks on leave.
		}

		// Move the user into their new channel.
		newID := created.ID()
		if _, err := client.Rest.UpdateMember(vs.GuildID, vs.UserID, discord.MemberUpdate{ChannelID: &newID}); err != nil {
			slog.Error("avc: move member", "user_id", vs.UserID, "channel_id", newID, "err", err)
		}

		// Post the owner's control panel in the voice channel's text chat.
		if _, err := client.Rest.CreateMessage(newID, shared.ControlPanel(vs.UserID)); err != nil {
			slog.Error("avc: post control panel", "channel_id", newID, "err", err)
		}
	})
}
