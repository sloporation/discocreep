// Commands to configure message purge and to purge a user's messages on demand.
package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/shared"
)

// PurgeCommand returns the /purge application command definition.
// Administrator only by default (and enforced in the handlers).
func PurgeCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "purge",
		Description:              "Delete users' messages from Discord (the audit log keeps its copy)",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionAdministrator),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "settings",
				Description: "Show or change message purge settings (both are off by default)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "on-leave",
						Description: "Delete a member's messages automatically when they leave",
					},
					discord.ApplicationCommandOptionBool{
						Name:        "admin-purge",
						Description: "Allow administrators to delete a user's messages with /purge user",
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "user",
				Description: "Delete every message a user has posted in this server",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        "user",
						Description: "Whose messages to delete (can be someone who already left: paste their ID)",
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleSettings returns the handler for /purge settings. Options that
// aren't given keep their current value; with none, it just shows them.
func HandleSettings(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		if !shared.IsAdmin(e.Member()) {
			return respond(e, "❌ Only administrators can change message purge settings.")
		}
		guildID := *e.GuildID()

		settings, err := shared.LoadSettings(e.Ctx, db, guildID)
		if err != nil {
			slog.Error("messagePurge: load settings", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}

		onLeave, setOnLeave := data.OptBool("on-leave")
		adminPurge, setAdminPurge := data.OptBool("admin-purge")
		changed := setOnLeave || setAdminPurge
		if setOnLeave {
			settings.OnLeave = onLeave
		}
		if setAdminPurge {
			settings.AdminPurge = adminPurge
		}

		if changed {
			if err := shared.SaveSettings(e.Ctx, db, guildID, settings); err != nil {
				slog.Error("messagePurge: save settings", "guild_id", guildID, "err", err)
				return respond(e, "❌ Database error — please try again.")
			}
			slog.Info("messagePurge: settings changed", "guild_id", guildID, "on_leave", settings.OnLeave, "admin_purge", settings.AdminPurge, "by", e.User().ID)
		}

		heading := "Message purge settings"
		if changed {
			heading = "✅ Message purge settings updated"
		}
		msg := fmt.Sprintf("**%s**\n%s **On leave**: %s\n%s **Admin purge** (`/purge user`): %s",
			heading,
			onOff(settings.OnLeave), describe(settings.OnLeave, "members' messages are deleted when they leave", "nothing is deleted when members leave"),
			onOff(settings.AdminPurge), describe(settings.AdminPurge, "administrators can delete a user's messages", "the command is disabled"),
		)
		if settings.OnLeave || settings.AdminPurge {
			msg += "\n\nThe bot needs **Manage Messages** in every channel it deletes from. The audit log keeps its copy of every message."
		}
		return respond(e, msg)
	}
}

// HandleUser returns the handler for /purge user. It only asks for
// confirmation; the purge starts from the confirm button.
func HandleUser(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		if !shared.IsAdmin(e.Member()) {
			return respond(e, "❌ Only administrators can purge messages.")
		}
		guildID := *e.GuildID()

		settings, err := shared.LoadSettings(e.Ctx, db, guildID)
		if err != nil {
			slog.Error("messagePurge: load settings", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}
		if !settings.AdminPurge {
			return respond(e, "❌ Admin purge is off. Turn it on with `/purge settings admin-purge:True` (this doesn't turn on purge on leave).")
		}

		user := data.User("user")
		if user.ID == e.Client().ID() {
			return respond(e, "❌ The bot won't purge its own messages.")
		}
		return e.CreateMessage(shared.ConfirmPrompt(user))
	}
}

func onOff(on bool) string {
	if on {
		return "🟢"
	}
	return "⚪"
}

func describe(on bool, ifOn, ifOff string) string {
	if on {
		return "on — " + ifOn
	}
	return "off — " + ifOff
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
