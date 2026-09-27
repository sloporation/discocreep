// When the user disconnects from a created voice channel, check if no one else
// is in it, then delete it if it's empty.
package events

import (
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleVoiceLeave returns a listener that deletes a temporary AVC channel
// once its last member leaves.
//
// Flow:
//  1. Read the previous channel from OldVoiceState — exit if the user wasn't
//     in a channel before this event (they joined for the first time, not left).
//  2. Skip if the channel didn't change (mute/deafen etc.).
//  3. Skip if the previous channel was not a bot-created AVC channel.
//  4. Count remaining members in that channel from the voice state cache.
//  5. If empty, delete the Discord channel and remove the avc_channels record.
func HandleVoiceLeave(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildVoiceStateUpdate) {
		// User wasn't in a channel before — this is a fresh join, not a leave.
		if e.OldVoiceState.ChannelID == nil {
			return
		}
		prevChannelID := *e.OldVoiceState.ChannelID
		guildID := e.VoiceState.GuildID

		// Channel didn't change (e.g. mute/deafen update).
		if e.VoiceState.ChannelID != nil && *e.VoiceState.ChannelID == prevChannelID {
			return
		}

		// The channel they left was not one we created.
		if !isAVCChannel(db, prevChannelID) {
			return
		}

		// disgo updates the voice state cache before dispatching the event, so
		// the leaving user is already gone from it.
		client := e.Client()
		for vs := range client.Caches.VoiceStates(guildID) {
			if vs.ChannelID != nil && *vs.ChannelID == prevChannelID {
				// At least one member remains — nothing to do.
				return
			}
		}

		// Channel is empty — delete it.
		if err := client.Rest.DeleteChannel(prevChannelID); err != nil {
			slog.Error("avc: delete channel", "channel_id", prevChannelID, "err", err)
			return
		}

		slog.Info("avc: deleted empty channel", "channel_id", prevChannelID)
		deleteAVCRecord(db, prevChannelID)
	})
}
