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

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// HandleVoiceJoin returns a VoiceStateUpdate handler that creates a personal
// voice channel whenever a user joins a monitored hub channel.
//
// Flow:
//  1. Ignore events that are not a join (ChannelID empty) or are joins into
//     an already-created AVC channel (prevents feedback loops).
//  2. Check the avc_monitors table — exit if the channel is not a hub.
//  3. Resolve the member's display name and the hub's parent category.
//  4. Create a new voice channel in that category (or top-level if none).
//  5. Record the new channel in avc_channels and move the user into it.
func HandleVoiceJoin(db *database.DB) func(*discordgo.Session, *discordgo.VoiceStateUpdate) {
	return func(s *discordgo.Session, e *discordgo.VoiceStateUpdate) {
		// Not a join/move — user disconnected.
		if e.ChannelID == "" {
			return
		}

		// User joined one of our own temp channels — nothing to create.
		if isAVCChannel(db, e.ChannelID) {
			return
		}

		// Not a monitored hub.
		if !isMonitored(db, e.GuildID, e.ChannelID) {
			return
		}

		// Fetch the hub channel so we know its parent category (may be "").
		hub, err := s.Channel(e.ChannelID)
		if err != nil {
			slog.Error("avc: fetch hub channel", "channel_id", e.ChannelID, "err", err)
			return
		}

		// Resolve a display name: server nickname → username.
		member, err := s.GuildMember(e.GuildID, e.UserID)
		if err != nil {
			slog.Error("avc: fetch guild member", "user_id", e.UserID, "err", err)
			return
		}
		displayName := member.Nick
		if displayName == "" {
			displayName = member.User.Username
		}

		// Create the temporary voice channel in the same category as the hub.
		// If the hub has no parent category, ParentID is "" and Discord places
		// the channel at the top level, which is the intended fallback.
		created, err := s.GuildChannelCreateComplex(e.GuildID, discordgo.GuildChannelCreateData{
			Name:     fmt.Sprintf("%s's Channel", displayName),
			Type:     discordgo.ChannelTypeGuildVoice,
			ParentID: hub.ParentID,
		})
		if err != nil {
			slog.Error("avc: create voice channel", "guild_id", e.GuildID, "err", err)
			return
		}

		slog.Info("avc: created channel", "channel", created.Name, "id", created.ID, "guild_id", e.GuildID)

		// Track the channel so the leave handler knows to clean it up.
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO avc_channels (channel_id, owner_id, guild_id) VALUES (?, ?, ?)",
			created.ID, e.UserID, e.GuildID,
		); err != nil {
			slog.Error("avc: insert avc_channels", "channel_id", created.ID, "err", err)
			// Non-fatal: still move the user; worst case the channel leaks on leave.
		}

		// Move the user into their new channel.
		if err := s.GuildMemberMove(e.GuildID, e.UserID, &created.ID); err != nil {
			slog.Error("avc: move member", "user_id", e.UserID, "channel_id", created.ID, "err", err)
		}
	}
}
