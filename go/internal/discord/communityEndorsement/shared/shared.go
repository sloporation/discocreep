// Package shared holds values used by more than one communityEndorsement
// subpackage: the sponsor button's ID and the sponsorship post's messages
// (posted by ./events, updated by ./components and ./events). It must not
// import any other communityEndorsement package, so that communityEndorsement
// and its subpackages can all depend on it without an import cycle.
package shared

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// SponsorButtonRoute is the router pattern for the sponsor button. {id} is
// the endorsements row ID, so a stale button from an earlier join can't
// sponsor a later one.
const SponsorButtonRoute = "/endorse/sponsor/{id}"

// SponsorButtonID returns the custom_id of the sponsor button for an endorsement.
func SponsorButtonID(endorsementID int64) string {
	return fmt.Sprintf("/endorse/sponsor/%d", endorsementID)
}

const (
	colorPending   = 0xFEE75C
	colorSponsored = 0x57F287
	colorLeft      = 0x99AAB5
)

// ThreadName is the forum thread title for a new joiner's sponsorship post.
func ThreadName(user discord.User) string {
	return fmt.Sprintf("Sponsor %s?", user.Username)
}

// PendingPost is the sponsorship message posted when a member joins. Mentions
// are only used inside the embed, where they never ping.
func PendingPost(endorsementID int64, user discord.User) discord.MessageCreate {
	return discord.MessageCreate{
		Embeds: []discord.Embed{{
			Color:       colorPending,
			Title:       "New arrival — do you want to sponsor this person?",
			Description: fmt.Sprintf("<@%s> (%s) has joined and is waiting to be let in.\n\nSponsoring them gives them access to the server, and you'll be recorded as the person who let them in.", user.ID, user.Username),
			Thumbnail:   &discord.EmbedResource{URL: user.EffectiveAvatarURL()},
			Fields: []discord.EmbedField{
				{Name: "Account created", Value: discord.FormattedTimestampMention(user.CreatedAt().Unix(), discord.TimestampStyleRelative)},
			},
		}},
		Components: []discord.LayoutComponent{
			discord.NewActionRow(
				discord.NewSuccessButton("Sponsor", SponsorButtonID(endorsementID)).
					WithEmoji(discord.NewComponentEmoji("🤝")),
			),
		},
	}
}

// Sponsored turns a pending post's embed into its sponsored state and removes the button.
func Sponsored(post discord.Embed, sponsorID snowflake.ID, at time.Time) discord.MessageUpdate {
	post.Color = colorSponsored
	post.Title = "Sponsored"
	post.Fields = append(post.Fields,
		discord.EmbedField{Name: "Let in by", Value: fmt.Sprintf("<@%s>", sponsorID)},
		discord.EmbedField{Name: "Sponsored", Value: discord.FormattedTimestampMention(at.Unix(), discord.TimestampStyleRelative)},
	)
	return closed(post)
}

// Left turns a pending post's embed into its "left before sponsorship" state
// and removes the button.
func Left(post discord.Embed) discord.MessageUpdate {
	post.Color = colorLeft
	post.Title = "Left before being sponsored"
	return closed(post)
}

// closed replaces the post's embed and clears its components.
func closed(post discord.Embed) discord.MessageUpdate {
	return discord.MessageUpdate{
		Embeds:     &[]discord.Embed{post},
		Components: &[]discord.LayoutComponent{},
	}
}
