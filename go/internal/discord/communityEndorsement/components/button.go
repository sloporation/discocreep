// Button handler for sponsoring a new joiner.
package components

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement/shared"
)

// HandleSponsor returns the handler for the Sponsor button: claim the
// endorsement for the clicker, grant the member role, and update the post.
func HandleSponsor(db *database.DB) handler.ButtonComponentHandler {
	return func(_ discord.ButtonInteractionData, e *handler.ComponentEvent) error {
		endorsementID, err := strconv.ParseInt(e.Vars["id"], 10, 64)
		if err != nil {
			return ephemeral(e, "❌ This button is broken.")
		}
		sponsorID := e.User().ID

		var (
			guildID, userID  snowflake.ID
			status           string
			currentSponsorID *snowflake.ID
			memberRoleID     *snowflake.ID
		)
		err = db.QueryRowContext(e.Ctx, `
			SELECT en.guild_id, en.user_id, en.status, en.sponsor_id, c.member_role_id
			FROM endorsements en
			LEFT JOIN endorsement_configs c ON c.guild_id = en.guild_id
			WHERE en.id = ?`,
			endorsementID,
		).Scan(&guildID, &userID, &status, &currentSponsorID, &memberRoleID)
		if errors.Is(err, sql.ErrNoRows) {
			return ephemeral(e, "❌ This sponsorship request no longer exists.")
		}
		if err != nil {
			slog.Error("communityEndorsement: load endorsement", "id", endorsementID, "err", err)
			return ephemeral(e, "❌ Database error — please try again.")
		}

		switch {
		case status == "sponsored" && currentSponsorID != nil:
			return ephemeral(e, fmt.Sprintf("<@%s> has already been sponsored by <@%s>.", userID, *currentSponsorID))
		case status == "left":
			return ephemeral(e, fmt.Sprintf("<@%s> left before being sponsored.", userID))
		case userID == sponsorID:
			return ephemeral(e, "❌ You can't sponsor yourself.")
		case memberRoleID == nil:
			return ephemeral(e, "❌ Community endorsement isn't configured for this server any more.")
		}

		// Claim it atomically so two people clicking at once can't both sponsor.
		res, err := db.ExecContext(e.Ctx,
			"UPDATE endorsements SET status = 'sponsored', sponsor_id = ?, sponsored_at = NOW() WHERE id = ? AND status = 'pending'",
			sponsorID, endorsementID,
		)
		if err != nil {
			slog.Error("communityEndorsement: claim endorsement", "id", endorsementID, "err", err)
			return ephemeral(e, "❌ Database error — please try again.")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ephemeral(e, fmt.Sprintf("Someone else just sponsored <@%s>.", userID))
		}

		if err := e.Client().Rest.AddMemberRole(guildID, userID, *memberRoleID); err != nil {
			slog.Error("communityEndorsement: grant member role", "guild_id", guildID, "user_id", userID, "role_id", *memberRoleID, "err", err)
			// Undo the claim so someone can try again once the role is fixed.
			if _, err := db.ExecContext(context.Background(),
				"UPDATE endorsements SET status = 'pending', sponsor_id = NULL, sponsored_at = NULL WHERE id = ?",
				endorsementID,
			); err != nil {
				slog.Error("communityEndorsement: undo claim", "id", endorsementID, "err", err)
			}
			return ephemeral(e, fmt.Sprintf("❌ Couldn't give <@%s> the <@&%s> role. Check the bot has Manage Roles and its role is above that one.", userID, *memberRoleID))
		}

		slog.Info("communityEndorsement: sponsored", "guild_id", guildID, "user_id", userID, "sponsor_id", sponsorID, "id", endorsementID)

		// Edit the post in place: sponsored embed, button removed.
		var embed discord.Embed
		if embeds := e.Message.Embeds; len(embeds) > 0 {
			embed = embeds[0]
		}
		return e.UpdateMessage(shared.Sponsored(embed, sponsorID, time.Now()))
	}
}

// ephemeral sends a reply visible only to the user who pressed the button.
func ephemeral(e *handler.ComponentEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
