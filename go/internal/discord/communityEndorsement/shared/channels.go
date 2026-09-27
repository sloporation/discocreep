package shared

import (
	"fmt"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// GuildChannel returns a guild channel from cache, falling back to REST.
func GuildChannel(client *bot.Client, channelID snowflake.ID) (discord.GuildChannel, error) {
	if ch, ok := client.Caches.Channel(channelID); ok {
		return ch, nil
	}
	ch, err := client.Rest.GetChannel(channelID)
	if err != nil {
		return nil, err
	}
	gc, ok := ch.(discord.GuildChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s is not a guild channel", channelID)
	}
	return gc, nil
}

// ForumTags returns a forum channel's available tags.
func ForumTags(client *bot.Client, forumID snowflake.ID) ([]discord.ChannelTag, error) {
	ch, err := GuildChannel(client, forumID)
	if err != nil {
		return nil, err
	}
	forum, ok := ch.(discord.GuildForumChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s is not a forum", forumID)
	}
	return forum.AvailableTags, nil
}
