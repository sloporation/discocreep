// Button handlers for the control panel posted in each auto voice channel's
// text chat. The buttons live in the voice channel's own chat, so the
// interaction's channel is the voice channel being controlled.
package components

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/shared"
)

// accessPerms is what hiding denies to everyone else and grants to the
// members who were connected at the time.
const accessPerms = discord.PermissionViewChannel | discord.PermissionConnect

// botPerms keeps the bot able to manage (and later delete) a hidden channel
// even if it isn't an administrator.
const botPerms = accessPerms | discord.PermissionManageChannels | discord.PermissionMoveMembers

// HandleHide returns the handler for the Hide button: deny View/Connect to
// @everyone and every role, and grant it to each member currently connected,
// plus the owner and the bot.
func HandleHide(db *database.DB, alerter *alerts.Alerter) handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		channelID := e.Channel().ID()
		if ok, err := checkOwner(e, db, channelID); !ok {
			return err
		}
		if err := e.DeferCreateMessage(true); err != nil {
			return fmt.Errorf("defer hide: %w", err)
		}

		client := e.Client()
		guildID := *e.GuildID()
		ch, err := guildChannel(client, channelID)
		if err != nil {
			slog.Error("avc: fetch channel for hide", "channel_id", channelID, "err", err)
			return respondEdit(e, "❌ Could not fetch this channel.")
		}

		// Role overwrites (including @everyone, whose role ID is the guild
		// ID) keep their other bits but lose View/Connect. Existing member
		// overwrites are dropped; members are re-added below.
		overwrites := []discord.PermissionOverwrite{
			discord.RolePermissionOverwrite{RoleID: guildID, Deny: accessPerms},
		}
		for _, ow := range ch.PermissionOverwrites() {
			role, ok := ow.(discord.RolePermissionOverwrite)
			if !ok {
				continue
			}
			hidden := discord.RolePermissionOverwrite{
				RoleID: role.RoleID,
				Allow:  role.Allow.Remove(accessPerms),
				Deny:   role.Deny.Add(accessPerms),
			}
			if role.RoleID == guildID {
				overwrites[0] = hidden
			} else {
				overwrites = append(overwrites, hidden)
			}
		}

		// Members connected right now, plus the owner and the bot.
		allowed := map[snowflake.ID]discord.Permissions{
			e.User().ID: accessPerms,
			client.ID(): botPerms,
		}
		for vs := range client.Caches.VoiceStates(guildID) {
			if vs.ChannelID != nil && *vs.ChannelID == channelID {
				allowed[vs.UserID] |= accessPerms
			}
		}
		for userID, perms := range allowed {
			overwrites = append(overwrites, discord.MemberPermissionOverwrite{UserID: userID, Allow: perms})
		}

		if err := setOverwrites(client, channelID, overwrites); err != nil {
			slog.Error("avc: hide channel", "channel_id", channelID, "err", err)
			alertPermissions(alerter, client, guildID, channelID, "hide", err)
			return respondEdit(e, "❌ Failed to hide the channel.")
		}

		members := len(allowed) - 1 // not counting the bot
		slog.Info("avc: hid channel", "channel_id", channelID, "members", members)
		return respondEdit(e, fmt.Sprintf("🙈 Channel hidden. %d member(s) can still see and join it.", members))
	}
}

// HandleUnhide returns the handler for the Unhide button: reset the channel's
// overwrites to its category's (or none, if it has no category).
func HandleUnhide(db *database.DB, alerter *alerts.Alerter) handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		channelID := e.Channel().ID()
		if ok, err := checkOwner(e, db, channelID); !ok {
			return err
		}
		if err := e.DeferCreateMessage(true); err != nil {
			return fmt.Errorf("defer unhide: %w", err)
		}

		client := e.Client()
		ch, err := guildChannel(client, channelID)
		if err != nil {
			slog.Error("avc: fetch channel for unhide", "channel_id", channelID, "err", err)
			return respondEdit(e, "❌ Could not fetch this channel.")
		}

		overwrites := []discord.PermissionOverwrite{}
		if parentID := ch.ParentID(); parentID != nil {
			parent, err := guildChannel(client, *parentID)
			if err != nil {
				slog.Error("avc: fetch parent category", "channel_id", *parentID, "err", err)
				return respondEdit(e, "❌ Could not fetch the channel's category.")
			}
			overwrites = append(overwrites, parent.PermissionOverwrites()...)
		}

		if err := setOverwrites(client, channelID, overwrites); err != nil {
			slog.Error("avc: unhide channel", "channel_id", channelID, "err", err)
			alertPermissions(alerter, client, *e.GuildID(), channelID, "unhide", err)
			return respondEdit(e, "❌ Failed to unhide the channel.")
		}

		slog.Info("avc: unhid channel", "channel_id", channelID)
		return respondEdit(e, "👀 Channel is visible again.")
	}
}

// HandleRenameButton returns the handler for the Rename button, which opens
// a modal pre-filled with the current name.
func HandleRenameButton(db *database.DB) handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		channelID := e.Channel().ID()
		if ok, err := checkOwner(e, db, channelID); !ok {
			return err
		}

		input := discord.NewShortTextInput(shared.RenameInputID).
			WithRequired(true).
			WithMinLength(1).
			WithMaxLength(100)
		if ch, ok := e.Client().Caches.Channel(channelID); ok {
			input = input.WithValue(ch.Name())
		}

		return e.Modal(discord.NewModalCreate(shared.RenameModalID, "Rename channel",
			discord.NewLabel("Channel name", input),
		))
	}
}
