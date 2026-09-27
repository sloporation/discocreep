// Package shared holds values used by more than one avc subpackage (the
// control panel's component IDs and message). It must not import any other
// avc package, so that avc, ./events and ./components can all depend on it
// without an import cycle.
package shared

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// Feature names this feature in admin alerts.
const Feature = "avc"

// Routed component IDs (the router only dispatches custom_ids starting with "/").
const (
	HideButtonID   = "/avc/hide"
	UnhideButtonID = "/avc/unhide"
	RenameButtonID = "/avc/rename"
	RenameModalID  = "/avc/rename-modal"

	// RenameInputID is the modal's text field; read from the submission, not routed.
	RenameInputID = "avc_rename_input"
)

// ControlPanel is the message posted in a newly created AVC channel's text
// chat, giving its owner hide/unhide/rename buttons.
func ControlPanel(ownerID snowflake.ID) discord.MessageCreate {
	return discord.MessageCreate{
		Content: fmt.Sprintf("<@%s>", ownerID),
		Embeds: []discord.Embed{{
			Color:       0x5865F2,
			Title:       "🔊 Channel controls",
			Description: "This is your channel. Only you can use these buttons.\n\n**Hide** — only people connected right now can see and join it (they keep access if they drop out).\n**Unhide** — reset the channel to its category's permissions.\n**Rename** — change the channel name.",
		}},
		Components: []discord.LayoutComponent{
			discord.NewActionRow(
				discord.NewSecondaryButton("Hide", HideButtonID).WithEmoji(discord.NewComponentEmoji("🙈")),
				discord.NewSecondaryButton("Unhide", UnhideButtonID).WithEmoji(discord.NewComponentEmoji("👀")),
				discord.NewSecondaryButton("Rename", RenameButtonID).WithEmoji(discord.NewComponentEmoji("✏️")),
			),
		},
	}
}
