// Package wowSync keeps a Discord server in step with a retail WoW guild.
//
// Admins configure it in the web dashboard: which WoW guild, whether to set
// members' nicknames to their character name, and which Discord role each
// guild rank gets. Members choose their primary character per server in the
// dashboard (wow_primary_characters). Every 15 minutes, or soon after
// settings change / "Sync now" / a member changes character, the worker
// reads the guild roster and updates those members.
//
// Guardrails: only roles in the rank → role mapping are ever added or
// removed; members without a primary character are never touched; if the
// roster can't be read nothing changes (and admins are alerted once). The
// bot never creates or deletes roles.
//
// Needs the Battle.net client (BXT_BATTLENET_CLIENT_ID/_SECRET) for the
// app-only roster API; without it the feature doesn't run.
//
// Wiring: Register(bot) starts the sync loop from ./shared.
package wowSync

import (
	"log/slog"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/wowSync/shared"
)

// Register wires WoW guild sync into the bot.
// Must be called after the bot is created but before bot.Run.
func Register(bot *discord.Bot) {
	bn := bot.Cfg.BattleNet
	if bn.ClientID == "" || bn.ClientSecret == "" {
		slog.Info("wowSync: Battle.net not configured; WoW guild sync is off")
		return
	}
	syncer := shared.NewSyncer(bot.DB, bot.Alerts, bot.Locks, blizzard.New(bn.ClientID, bn.ClientSecret))

	// Sync due servers (the ones in this worker's partitions) every minute.
	bot.AddStartHook(syncer.Loop(bot.Client))
}
