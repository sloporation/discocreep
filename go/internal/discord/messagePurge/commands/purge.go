// Commands to enable message purge and to purge a user's messages on demand.
package commands

import (
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
				Name:        "enable",
				Description: "Enable message purge: members' messages are deleted when they leave",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "disable",
				Description: "Disable message purge (running purges finish)",
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

// HandleEnable returns the handler for /purge enable.
func HandleEnable(db *database.DB) handler.SlashCommandHandler {
	return func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		return setEnabled(db, e, true)
	}
}

// HandleDisable returns the handler for /purge disable.
func HandleDisable(db *database.DB) handler.SlashCommandHandler {
	return func(_ discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		return setEnabled(db, e, false)
	}
}

func setEnabled(db *database.DB, e *handler.CommandEvent, enabled bool) error {
	if !shared.IsAdmin(e.Member()) {
		return respond(e, "❌ Only administrators can change message purge.")
	}
	guildID := *e.GuildID()

	if _, err := db.ExecContext(e.Ctx, `
		INSERT INTO purge_configs (guild_id, enabled) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE enabled = VALUES(enabled)`,
		guildID, enabled,
	); err != nil {
		slog.Error("messagePurge: save config", "guild_id", guildID, "err", err)
		return respond(e, "❌ Database error — please try again.")
	}

	slog.Info("messagePurge: config changed", "guild_id", guildID, "enabled", enabled, "by", e.User().ID)
	if enabled {
		return respond(e, "✅ Message purge enabled. When a member leaves, their messages are deleted from Discord. Admins can also use `/purge user`.\n\n"+
			"The bot needs **Manage Messages** in every channel to delete from. The audit log keeps its copy of every message.")
	}
	return respond(e, "✅ Message purge disabled. Purges already running will finish.")
}

// HandleUser returns the handler for /purge user. It only asks for
// confirmation; the purge starts from the confirm button.
func HandleUser(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		if !shared.IsAdmin(e.Member()) {
			return respond(e, "❌ Only administrators can purge messages.")
		}
		guildID := *e.GuildID()

		enabled, err := shared.Enabled(e.Ctx, db, guildID)
		if err != nil {
			slog.Error("messagePurge: check enabled", "guild_id", guildID, "err", err)
			return respond(e, "❌ Database error — please try again.")
		}
		if !enabled {
			return respond(e, "❌ Message purge isn't enabled. Run `/purge enable` first.")
		}

		user := data.User("user")
		if user.ID == e.Client().ID() {
			return respond(e, "❌ The bot won't purge its own messages.")
		}
		return e.CreateMessage(shared.ConfirmPrompt(user))
	}
}

// respond sends an ephemeral reply visible only to the command issuer.
func respond(e *handler.CommandEvent, msg string) error {
	return e.CreateMessage(discord.MessageCreate{
		Content: msg,
		Flags:   discord.MessageFlagEphemeral,
	})
}
