// Package discord owns the bot's connection to Discord.
//
// It exposes a Bot type that wraps a disgo *bot.Client along with shared
// dependencies (database, config) and provides a small registration API that
// feature packages call from their own Register() functions. The split lets us
// keep cross-cutting concerns (client lifecycle, slash-command sync, the
// interaction router) in one place while every feature (avc, loginLogger, ...)
// stays self-contained in its own subpackage.
//
// Typical wiring from cmd/bxt:
//
//	b, _ := discord.New(cfg, db)
//	avc.Register(b)
//	loginLogger.Register(b)
//	b.Run(ctx)
//
// Interactions are routed by path through disgo's handler.Mux:
//   - slash commands:        "/<command>" or "/<command>/<subcommand>"
//   - components and modals: the custom_id, which must start with "/"
//     (e.g. "/ticket/create"). custom_ids without a leading "/" are ignored.
package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// shutdownTimeout bounds how long Run waits for the gateway to close cleanly.
const shutdownTimeout = 10 * time.Second

// Bot is the runtime container shared by every feature package.
//
// Client, DB, Cfg, Alerts, and Router are public on purpose: feature handlers need
// them. Command definitions are private — features add them via AddCommand so
// Run can sync them to Discord in one call.
type Bot struct {
	Client *bot.Client
	DB     *database.DB
	Cfg    config.Config

	// Alerts posts to each guild's admin alerts channel. Pass it into
	// handler constructors (like DB) for failures an admin needs to fix.
	Alerts *alerts.Alerter

	// Router dispatches commands, components, and modals by path. Features
	// register their handlers on it, e.g. b.Router.SlashCommand("/avc/watch", h).
	Router *handler.Mux

	commands []discord.ApplicationCommandCreate
	guildID  snowflake.ID
}

// New constructs a Bot with the client pre-configured (intents, caches, event
// listeners) but not yet connected. Call Run to actually connect.
func New(cfg config.Config, db *database.DB) (*Bot, error) {
	if cfg.Discord.Token == "" {
		return nil, errors.New("discord token is empty")
	}

	var guildID snowflake.ID
	if cfg.Discord.GuildID != "" {
		id, err := snowflake.Parse(cfg.Discord.GuildID)
		if err != nil {
			return nil, fmt.Errorf("parse discord.guild_id: %w", err)
		}
		guildID = id
	}

	router := handler.New()
	router.Error(func(e *handler.InteractionEvent, err error) {
		slog.Error("interaction handler failed", "type", e.Type(), "err", err)
	})
	router.NotFound(func(e *handler.InteractionEvent) error {
		slog.Warn("no handler for interaction", "type", e.Type())
		return nil
	})

	client, err := disgo.New(cfg.Discord.Token,
		// Intents: guild metadata, voice state updates, guild members, and
		// guild messages/reactions (for audit).
		//
		// GuildMembers and MessageContent are privileged: "Server Members Intent"
		// and "Message Content Intent" must be enabled in the Developer Portal,
		// or the gateway closes with 4014 (Disallowed intents). The others are
		// not privileged.
		bot.WithGatewayConfigOpts(gateway.WithIntents(
			gateway.IntentGuilds,
			gateway.IntentGuildVoiceStates,
			gateway.IntentGuildMembers,
			gateway.IntentGuildMessages,
			gateway.IntentGuildMessageReactions,
			gateway.IntentMessageContent,
		)),
		// Voice states must be cached for disgo to emit GuildVoiceJoin/Move/Leave
		// with the previous state; members for display names; channels and roles
		// so permission and category lookups don't need a REST call.
		bot.WithCacheConfigOpts(cache.WithCaches(
			cache.FlagGuilds,
			cache.FlagChannels,
			cache.FlagRoles,
			cache.FlagMembers,
			cache.FlagVoiceStates,
		)),
		// Run each event in its own goroutine so a slow handler (DB, REST) can't
		// stall the gateway.
		bot.WithEventManagerConfigOpts(bot.WithAsyncEventsEnabled()),
		bot.WithEventListeners(router),
		bot.WithEventListenerFunc(func(e *events.Ready) {
			slog.Info("discord connected", "user", e.User.Username, "guilds", len(e.Guilds))
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("disgo.New: %w", err)
	}

	return &Bot{
		Client:  client,
		DB:      db,
		Cfg:     cfg,
		Alerts:  alerts.New(db),
		Router:  router,
		guildID: guildID,
	}, nil
}

// AddCommand queues an application command definition to be synced to Discord
// when Run starts. Register its handler(s) separately on b.Router.
func (b *Bot) AddCommand(cmd discord.ApplicationCommandCreate) {
	b.commands = append(b.commands, cmd)
}

// AddListener registers a gateway event listener (voice state updates, member
// joins, etc.). Build one with bot.NewListenerFunc(func(e *events.X) {...}).
func (b *Bot) AddListener(l bot.EventListener) {
	b.Client.AddEventListeners(l)
}

// Run syncs slash commands, opens the gateway, and blocks until ctx is
// cancelled, then closes the client.
//
// Commands are bulk-overwritten: if discord.guild_id is set they go to that
// guild (instant); otherwise they are registered globally (can take up to an
// hour to propagate). Either way, commands no longer defined here are removed.
func (b *Bot) Run(ctx context.Context) error {
	var guildIDs []snowflake.ID
	if b.guildID != 0 {
		guildIDs = []snowflake.ID{b.guildID}
	}
	if err := handler.SyncCommands(b.Client, b.commands, guildIDs); err != nil {
		return fmt.Errorf("sync commands: %w", err)
	}
	slog.Info("commands synced", "count", len(b.commands), "guild", b.Cfg.Discord.GuildID)

	if err := b.Client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}

	<-ctx.Done()
	slog.Info("shutting down discord client")

	// ctx is already cancelled, so give Close its own deadline.
	closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	b.Client.Close(closeCtx)
	return nil
}
