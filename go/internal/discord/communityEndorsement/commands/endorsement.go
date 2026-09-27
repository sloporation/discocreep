// Commands to configure community endorsement for a guild.
package commands

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// EndorsementCommand returns the /endorsement application command definition
// with setup and disable subcommands. Administrator only by default.
func EndorsementCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "endorsement",
		Description:              "Configure community endorsement (new joiners need a sponsor)",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionAdministrator),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "setup",
				Description: "Enable community endorsement and choose where sponsorship requests go",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionChannel{
						Name:         "channel",
						Description:  "Text channel (posts a message) or forum (opens a thread) for sponsorship requests",
						Required:     true,
						ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildText, discord.ChannelTypeGuildForum},
					},
					discord.ApplicationCommandOptionRole{
						Name:        "role",
						Description: "Role granted when someone sponsors a new joiner",
						Required:    true,
					},
					discord.ApplicationCommandOptionString{
						Name:         "tag",
						Description:  "Forum only: tag to apply to new joiner threads",
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "disable",
				Description: "Stop posting sponsorship requests for new joiners",
			},
		},
	}
}

// HandleSetup returns the handler for /endorsement setup.
func HandleSetup(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		guildID := *e.GuildID()
		channel := data.Channel("channel")
		role := data.Role("role")

		if msg := checkRole(e.Client(), guildID, role); msg != "" {
			return respond(e, msg)
		}

		var tagID *snowflake.ID
		if tag, ok := data.OptString("tag"); ok && tag != "" {
			if channel.Type != discord.ChannelTypeGuildForum {
				return respond(e, "❌ Tags only apply when the channel is a forum.")
			}
			id, msg := resolveTag(e.Client(), channel.ID, tag)
			if msg != "" {
				return respond(e, msg)
			}
			tagID = &id
		}

		if _, err := db.ExecContext(e.Ctx, `
			INSERT INTO endorsement_configs (guild_id, enabled, channel_id, member_role_id, forum_tag_id)
			VALUES (?, TRUE, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
			enabled = TRUE,
			channel_id = VALUES(channel_id),
			member_role_id = VALUES(member_role_id),
			forum_tag_id = VALUES(forum_tag_id)`,
			guildID, channel.ID, role.ID, tagID,
		); err != nil {
			slog.Error("communityEndorsement: save config", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		slog.Info("communityEndorsement: enabled", "guild_id", guildID, "channel_id", channel.ID, "role_id", role.ID, "tag_id", tagID)

		where := fmt.Sprintf("a message in <#%s>", channel.ID)
		if channel.Type == discord.ChannelTypeGuildForum {
			where = fmt.Sprintf("a new thread in <#%s>", channel.ID)
		}
		return respond(e, fmt.Sprintf(
			"✅ Community endorsement enabled. New joiners get %s, and whoever sponsors them gives them <@&%s>.\n\n"+
				"⚠️ The bot does not configure your server for you, it just automates adding the role. You have to configure permissions to make this work.",
			where, role.ID,
		))
	}
}

// HandleDisable returns the handler for /endorsement disable.
func HandleDisable(db *database.DB) handler.SlashCommandHandler {
	return func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		guildID := *e.GuildID()
		res, err := db.ExecContext(e.Ctx,
			"UPDATE endorsement_configs SET enabled = FALSE WHERE guild_id = ? AND enabled",
			guildID,
		)
		if err != nil {
			slog.Error("communityEndorsement: disable", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return respond(e, "Community endorsement isn't enabled.")
		}

		slog.Info("communityEndorsement: disabled", "guild_id", guildID)
		return respond(e, "✅ Community endorsement disabled. Existing sponsor buttons still work.")
	}
}

// checkRole returns an error message if the bot can't grant role, or "" if it can.
func checkRole(client *bot.Client, guildID snowflake.ID, role discord.Role) string {
	if role.ID == guildID {
		return "❌ @everyone can't be the member role."
	}
	if role.Managed {
		return fmt.Sprintf("❌ <@&%s> is managed by an integration and can't be assigned.", role.ID)
	}

	// The bot can only assign roles below its own highest role.
	self, ok := client.Caches.SelfMember(guildID)
	if !ok {
		return "" // can't tell; the sponsor button reports failures
	}
	highest := 0
	for _, r := range client.Caches.MemberRoles(self) {
		highest = max(highest, r.Position)
	}
	if role.Position >= highest {
		return fmt.Sprintf("❌ <@&%s> is above the bot's highest role, so the bot can't assign it. Move the bot's role above it first.", role.ID)
	}
	return ""
}

// resolveTag finds a forum tag by ID (what autocomplete sends) or by name
// (if typed by hand). It returns the tag ID, or an error message.
func resolveTag(client *bot.Client, forumID snowflake.ID, tag string) (snowflake.ID, string) {
	tags, err := forumTags(client, forumID)
	if err != nil {
		slog.Error("communityEndorsement: fetch forum", "channel_id", forumID, "err", err)
		return 0, "❌ Could not fetch the forum's tags."
	}

	for _, t := range tags {
		if t.ID.String() == tag || strings.EqualFold(t.Name, tag) {
			return t.ID, ""
		}
	}

	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	if len(names) == 0 {
		return 0, fmt.Sprintf("❌ <#%s> has no tags. Create one in the forum's settings first.", forumID)
	}
	return 0, fmt.Sprintf("❌ No tag called %q in <#%s>. Available: %s", tag, forumID, strings.Join(names, ", "))
}

// forumTags returns a forum channel's available tags, from cache or REST.
func forumTags(client *bot.Client, forumID snowflake.ID) ([]discord.ChannelTag, error) {
	if ch, ok := client.Caches.Channel(forumID); ok {
		if forum, ok := ch.(discord.GuildForumChannel); ok {
			return forum.AvailableTags, nil
		}
	}
	ch, err := client.Rest.GetChannel(forumID)
	if err != nil {
		return nil, err
	}
	forum, ok := ch.(discord.GuildForumChannel)
	if !ok {
		return nil, fmt.Errorf("channel %s is not a forum", forumID)
	}
	return forum.AvailableTags, nil
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
