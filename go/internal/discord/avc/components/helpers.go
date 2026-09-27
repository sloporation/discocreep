package components

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc/shared"
)

// interactionEvent is the subset of *handler.ComponentEvent and
// *handler.ModalEvent needed to reply to the user.
type interactionEvent interface {
	User() discord.User
	CreateMessage(discord.MessageCreate, ...rest.RequestOpt) error
}

// responseEditor is satisfied by both *handler.ComponentEvent and *handler.ModalEvent.
type responseEditor interface {
	UpdateInteractionResponse(discord.MessageUpdate, ...rest.RequestOpt) (*discord.Message, error)
}

// checkOwner reports whether the user who triggered the interaction owns
// channelID. If not, it has already replied and the returned error is the
// reply's error.
func checkOwner(e interactionEvent, db *database.DB, channelID snowflake.ID) (bool, error) {
	var ownerID snowflake.ID
	err := db.QueryRowContext(context.Background(),
		"SELECT owner_id FROM avc_channels WHERE channel_id = ?",
		channelID,
	).Scan(&ownerID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, ephemeral(e, "❌ This isn't an auto voice channel.")
	case err != nil:
		slog.Error("avc: look up channel owner", "channel_id", channelID, "err", err)
		return false, ephemeral(e, "❌ Database error — please try again.")
	case ownerID != e.User().ID:
		return false, ephemeral(e, fmt.Sprintf("❌ Only <@%s> can change this channel.", ownerID))
	}
	return true, nil
}

// alertPermissions tells admins that a control panel action failed because
// of the bot's permissions on the channel.
func alertPermissions(alerter *alerts.Alerter, client *bot.Client, guildID, channelID snowflake.ID, action string, err error) {
	alerter.Send(client, guildID, alerts.Alert{
		Feature:     shared.Feature,
		Title:       "Voice channel control failed",
		Description: fmt.Sprintf("The owner of <#%s> tried to %s it, but the bot couldn't. Check the bot has Manage Channels and Manage Permissions (Manage Roles) there.", channelID, action),
		Fields:      []discord.EmbedField{alerts.ErrorField(err)},
	})
}

// guildChannel returns the channel from cache, falling back to REST.
func guildChannel(client *bot.Client, id snowflake.ID) (discord.GuildChannel, error) {
	if ch, ok := client.Caches.Channel(id); ok {
		return ch, nil
	}
	ch, err := client.Rest.GetChannel(id)
	if err != nil {
		return nil, err
	}
	gc, ok := ch.(discord.GuildChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s is not a guild channel", id)
	}
	return gc, nil
}

// setOverwrites replaces all of a voice channel's permission overwrites in one call.
func setOverwrites(client *bot.Client, channelID snowflake.ID, overwrites []discord.PermissionOverwrite) error {
	_, err := client.Rest.UpdateChannel(channelID, discord.GuildVoiceChannelUpdate{
		PermissionOverwrites: &overwrites,
	})
	return err
}

// ephemeral sends a reply visible only to the user who triggered the interaction.
func ephemeral(e interactionEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{Content: msg, Flags: discord.MessageFlagEphemeral})
}

// respondEdit edits the deferred interaction response.
func respondEdit(e responseEditor, msg string) error {
	_, err := e.UpdateInteractionResponse(discord.MessageUpdate{Content: &msg})
	return err
}
