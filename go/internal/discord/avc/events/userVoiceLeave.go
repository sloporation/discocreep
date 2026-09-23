// When the user disconnects from a created voice channel, check if no one else
// is in it, then delete it if it's empty.
package events

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleVoiceLeave returns a VoiceStateUpdate handler that deletes a temporary
// AVC channel once its last member leaves.
//
// Flow:
//  1. Read the previous channel from BeforeUpdate — exit if the user wasn't in
//     a channel before this event (they joined for the first time, not left).
//  2. Skip if they moved within the same channel (shouldn't happen, but guard).
//  3. Skip if the previous channel was not a bot-created AVC channel.
//  4. Count remaining voice-state members in that channel via cached guild state.
//  5. If empty, delete the Discord channel and remove the avc_channels record.
func HandleVoiceLeave(db *database.DB) func(*discordgo.Session, *discordgo.VoiceStateUpdate) {
	return func(s *discordgo.Session, e *discordgo.VoiceStateUpdate) {
		// Determine which channel the user was in before this update.
		var prevChannelID string
		if e.BeforeUpdate != nil {
			prevChannelID = e.BeforeUpdate.ChannelID
		}

		// User wasn't in a channel before — this is a fresh join, not a leave.
		if prevChannelID == "" {
			return
		}

		// Channel didn't change (e.g. mute/deafen update).
		if e.ChannelID == prevChannelID {
			return
		}

		// The channel they left was not one we created.
		if !isAVCChannel(db, prevChannelID) {
			return
		}

		// Count members still present in the vacated channel from cached state.
		// s.State.Guild is populated by the gateway and avoids an extra API call.
		guild, err := s.State.Guild(e.GuildID)
		if err != nil {
			// State unavailable — fall back to fetching from the API.
			ch, apiErr := s.Channel(prevChannelID)
			if apiErr != nil {
				// Channel is already gone; just clean up the DB record.
				deleteAVCRecord(db, prevChannelID)
				return
			}
			// Can't determine occupancy without state; log and bail to avoid
			// deleting a channel that might still have people in it.
			slog.Warn("avc: guild state unavailable, skipping empty check",
				"guild_id", e.GuildID, "channel_id", ch.ID, "err", err)
			return
		}

		for _, vs := range guild.VoiceStates {
			if vs.ChannelID == prevChannelID {
				// At least one member remains — nothing to do.
				return
			}
		}

		// Channel is empty — delete it.
		if _, err := s.ChannelDelete(prevChannelID); err != nil {
			slog.Error("avc: delete channel", "channel_id", prevChannelID, "err", err)
			return
		}

		slog.Info("avc: deleted empty channel", "channel_id", prevChannelID)
		deleteAVCRecord(db, prevChannelID)
	}
}
