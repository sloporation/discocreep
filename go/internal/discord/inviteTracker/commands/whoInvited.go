// Command for admins to look up who invited a member.
package commands

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// WhoInvitedCommand returns the /whoinvited application command definition.
// Administrator only by default.
func WhoInvitedCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "whoinvited",
		Description:              "See which invite a member joined with and who created it",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionAdministrator),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionUser{
				Name:        "user",
				Description: "The member to look up",
				Required:    true,
			},
		},
	}
}

// HandleWhoInvited returns the handler for /whoinvited. Every reply is
// ephemeral, so only the admin who ran it sees it.
func HandleWhoInvited(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		user := data.User("user")
		guildID := *e.GuildID()

		// Most recent join, plus how many times they've joined in total.
		var (
			code        sql.NullString
			inviterID   *snowflake.ID
			inviterName sql.NullString
			joinedAt    time.Time
			joins       int
		)
		err := db.QueryRowContext(e.Ctx, `
			SELECT invite_code, inviter_id, inviter_name, joined_at,
			       (SELECT COUNT(*) FROM invite_joins WHERE guild_id = ? AND user_id = ?)
			FROM invite_joins
			WHERE guild_id = ? AND user_id = ?
			ORDER BY joined_at DESC, id DESC
			LIMIT 1`,
			guildID, user.ID, guildID, user.ID,
		).Scan(&code, &inviterID, &inviterName, &joinedAt, &joins)
		if errors.Is(err, sql.ErrNoRows) {
			return respond(e, fmt.Sprintf("No join is recorded for <@%s>. They may have joined before invite tracking was added.", user.ID))
		}
		if err != nil {
			slog.Error("inviteTracker: look up join", "guild_id", guildID, "user_id", user.ID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		embed := discord.Embed{
			Color: 0x5865F2,
			Title: fmt.Sprintf("Who invited %s?", user.Username),
			Fields: []discord.EmbedField{
				{Name: "Member", Value: fmt.Sprintf("<@%s>", user.ID)},
				{Name: "Joined", Value: discord.FormattedTimestampMention(joinedAt.Unix(), discord.TimestampStyleLongDateTime)},
			},
		}

		switch {
		case !code.Valid:
			embed.Description = "The invite they used couldn't be determined (for example a vanity URL or Server Discovery)."
		case inviterID == nil:
			embed.Fields = append(embed.Fields,
				discord.EmbedField{Name: "Invite", Value: fmt.Sprintf("`%s`", code.String)},
				discord.EmbedField{Name: "Invited by", Value: "Unknown"},
			)
		default:
			embed.Fields = append(embed.Fields,
				discord.EmbedField{Name: "Invite", Value: fmt.Sprintf("`%s`", code.String)},
				discord.EmbedField{Name: "Invited by", Value: fmt.Sprintf("<@%s> (%s)", *inviterID, inviterName.String)},
			)
		}

		if joins > 1 {
			embed.Footer = &discord.EmbedFooter{Text: fmt.Sprintf("Showing their most recent of %d joins.", joins)}
		}

		return e.CreateMessage(discord.MessageCreate{
			Embeds: []discord.Embed{embed},
			Flags:  discord.MessageFlagEphemeral,
		})
	}
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
