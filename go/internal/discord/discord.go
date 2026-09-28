// Package discord is the worker runtime: it runs every feature against
// gateway events handed over by the watcher (see internal/queue).
//
// It exposes a Bot type that wraps a disgo *bot.Client along with shared
// dependencies (database, config, alerts, locks) and a small registration API
// that feature packages call from their own Register() functions. Features
// don't know events arrive through a queue: the worker feeds each event
// through disgo exactly as a live gateway would, so caches, listeners and
// the interaction router all behave normally, and replies go straight to
// Discord's REST API.
//
// Typical wiring from cmd/worker:
//
//	b, _ := discord.New(cfg, db, rdb)
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/locks"
	"gitlab.com/jacxb/bots/bxt/go/internal/queue"
)

// commandSyncTTL is how long a synced set of command definitions is
// remembered, so several workers starting together sync it only once.
const commandSyncTTL = 10 * time.Minute

// Bot is the runtime container shared by every feature package.
//
// Client, DB, Cfg, Alerts, Locks, Gateway and Router are public on purpose:
// feature handlers need them. Command definitions are private — features add
// them via AddCommand so Run can sync them to Discord in one call.
type Bot struct {
	Client *bot.Client
	DB     *database.DB
	Cfg    config.Config

	// Alerts posts to each guild's admin alerts channel. Pass it into
	// handler constructors (like DB) for failures an admin needs to fix.
	Alerts *alerts.Alerter

	// Locks hands out leases shared by every worker process. Use it when
	// work must run on exactly one worker (e.g. a long background job).
	Locks *locks.Locker

	// Gateway reports whether the watcher's gateway is connected.
	Gateway *queue.GatewayStatus

	// Router dispatches commands, components, and modals by path. Features
	// register their handlers on it, e.g. b.Router.SlashCommand("/avc/watch", h).
	Router *handler.Mux

	rdb        *redis.Client
	commands   []discord.ApplicationCommandCreate
	startHooks []func(ctx context.Context)
	guildID    snowflake.ID

	gatewaysMu sync.Mutex
	gateways   map[int]gateway.Gateway // per shard; never opened, see shardGateway
}

// New constructs a Bot with the client pre-configured (caches, listeners,
// router). It never connects to the gateway: events come from the queue.
func New(cfg config.Config, db *database.DB, rdb *redis.Client) (*Bot, error) {
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
		bot.WithCacheConfigOpts(cache.WithCaches(CacheFlags...)),
		// Run each event in its own goroutine so a slow handler (DB, REST)
		// can't hold up the partition. disgo still applies cache updates in
		// order before dispatching, so caches stay consistent.
		bot.WithEventManagerConfigOpts(bot.WithAsyncEventsEnabled()),
		bot.WithEventListeners(router),
	)
	if err != nil {
		return nil, fmt.Errorf("disgo.New: %w", err)
	}

	return &Bot{
		Client:   client,
		DB:       db,
		Cfg:      cfg,
		Alerts:   alerts.New(db),
		Locks:    locks.New(rdb),
		Gateway:  queue.NewGatewayStatus(rdb),
		Router:   router,
		rdb:      rdb,
		guildID:  guildID,
		gateways: make(map[int]gateway.Gateway),
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

// AddStartHook registers fn to run in its own goroutine when the worker
// starts. Use it for background work that used to hang off the gateway's
// Ready event (heartbeats, resuming jobs); fn should return when ctx is done.
func (b *Bot) AddStartHook(fn func(ctx context.Context)) {
	b.startHooks = append(b.startHooks, fn)
}

// Run syncs slash commands, starts the start hooks, and processes events
// from the queue until ctx is cancelled.
//
// Commands are bulk-overwritten: if discord.guild_id is set they go to that
// guild (instant); otherwise they are registered globally (can take up to an
// hour to propagate). Either way, commands no longer defined here are removed.
// With several workers, only the first to start with a given set of
// definitions syncs them.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.loadSelfUser(); err != nil {
		return fmt.Errorf("load bot user: %w", err)
	}
	if err := b.syncCommands(ctx); err != nil {
		return fmt.Errorf("sync commands: %w", err)
	}

	for _, hook := range b.startHooks {
		go hook(ctx)
	}

	consumer := queue.NewConsumer(b.rdb, b.Locks, b.Cfg.Queue.Partitions, b.handleEvent)
	consumer.Run(ctx)
	return nil
}

// loadSelfUser fills in the bot's own user, which disgo normally learns from
// the gateway's Ready event. Features rely on it via client.ID().
func (b *Bot) loadSelfUser() error {
	var user discord.OAuth2User
	if err := b.Client.Rest.Do(rest.GetCurrentUser.Compile(nil), nil, &user); err != nil {
		return err
	}
	b.Client.Caches.SetSelfUser(user)
	slog.Info("worker running as", "user", user.Username, "id", user.ID)
	return nil
}

func (b *Bot) syncCommands(ctx context.Context) error {
	defs, err := json.Marshal(b.commands)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(append(defs, []byte(b.guildID.String())...))
	first, err := b.Locks.Once(ctx, "commands:"+hex.EncodeToString(sum[:8]), commandSyncTTL)
	if err != nil {
		return err
	}
	if !first {
		slog.Info("commands already synced by another worker", "count", len(b.commands))
		return nil
	}

	var guildIDs []snowflake.ID
	if b.guildID != 0 {
		guildIDs = []snowflake.ID{b.guildID}
	}
	if err := handler.SyncCommands(b.Client, b.commands, guildIDs); err != nil {
		return err
	}
	slog.Info("commands synced", "count", len(b.commands), "guild", b.Cfg.Discord.GuildID)
	return nil
}

// handleEvent feeds one queued gateway event through disgo, which updates
// the cache and dispatches it to listeners and the interaction router.
func (b *Bot) handleEvent(_ context.Context, env queue.Envelope) error {
	eventType := gateway.EventType(env.Type)
	data, err := gateway.UnmarshalEventData(env.Data, eventType)
	if err != nil {
		return fmt.Errorf("unmarshal %s: %w", env.Type, err)
	}
	if _, unknown := data.(gateway.EventUnknown); unknown {
		return nil
	}

	if eventType == gateway.EventTypeInteractionCreate {
		if age := time.Since(env.ReceivedAt); age > 3*time.Second {
			// Discord will reject the reply; still try, in case of clock skew.
			slog.Warn("interaction reached worker late", "age", age.Round(time.Millisecond))
		}
	}

	// Make disgo emit the same guild event the watcher saw (GuildReady,
	// GuildAvailable or GuildJoin); it decides from these cache flags.
	if eventType == gateway.EventTypeGuildCreate && env.GuildID != 0 {
		b.Client.Caches.SetGuildUnready(env.GuildID, env.GuildCreate == queue.GuildReady)
		b.Client.Caches.SetGuildUnavailable(env.GuildID, env.GuildCreate == queue.GuildAvailable)
	}

	b.Client.EventManager.HandleGatewayEvent(b.shardGateway(env.ShardID), eventType, env.Seq, data)
	return nil
}

// shardGateway returns a stand-in gateway for a shard. It is never opened;
// disgo only asks it for its shard ID when dispatching events.
func (b *Bot) shardGateway(shardID int) gateway.Gateway {
	b.gatewaysMu.Lock()
	defer b.gatewaysMu.Unlock()
	gw, ok := b.gateways[shardID]
	if !ok {
		gw = gateway.New(b.Cfg.Discord.Token, nil, gateway.WithShardID(shardID), gateway.WithIntents(Intents...))
		b.gateways[shardID] = gw
	}
	return gw
}
