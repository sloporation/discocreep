// Package watcher holds the Discord gateway connection and publishes every
// event it receives to the queue (internal/queue) for workers to process.
//
// It runs no features and needs no database. Besides forwarding events it:
//   - keeps disgo's cache, so it can answer a worker's "resync partition N"
//     request with a snapshot of that partition's guilds (a worker that takes
//     over a partition needs the guilds' channels, roles and voice states)
//   - publishes a "gateway up" signal workers read (see queue.GatewayStatus)
package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/gateway"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	bxtdiscord "gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/queue"
)

const (
	// aliveInterval is how often the "gateway up" signal is refreshed.
	aliveInterval = 10 * time.Second
	// shutdownTimeout bounds how long Run waits for the gateway to close.
	shutdownTimeout = 10 * time.Second
)

// Watcher forwards gateway events to the queue.
type Watcher struct {
	client    *bot.Client
	publisher *queue.Publisher

	// mu makes "apply an event to the cache, then publish it" atomic with
	// respect to building a snapshot, so a snapshot is never published
	// after an event it doesn't include (which would roll a worker's cache
	// back). Only the gateway goroutine and resync take it.
	mu sync.Mutex
	// raw holds the raw payload of the dispatch being handled: disgo hands
	// over the raw event just before the parsed one.
	raw     *gateway.EventRaw
	rawData []byte
}

// New builds the gateway client. Call Run to connect.
func New(cfg config.Config, publisher *queue.Publisher) (*Watcher, error) {
	if cfg.Discord.Token == "" {
		return nil, fmt.Errorf("discord token is empty")
	}
	w := &Watcher{publisher: publisher}

	client, err := disgo.New(cfg.Discord.Token,
		bot.WithCacheConfigOpts(cache.WithCaches(bxtdiscord.CacheFlags...)),
		// Listeners stay synchronous (the default) so events are published
		// in the order the gateway delivered them.
	)
	if err != nil {
		return nil, fmt.Errorf("disgo.New: %w", err)
	}

	// Build the gateway ourselves (disgo.New does the same when given
	// gateway options) so every event goes through w.handleGatewayEvent.
	gw, err := client.Rest.GetGateway()
	if err != nil {
		return nil, fmt.Errorf("get gateway URL: %w", err)
	}
	client.Gateway = gateway.New(cfg.Discord.Token, w.handleGatewayEvent,
		gateway.WithURL(gw.URL),
		gateway.WithIntents(bxtdiscord.Intents...),
		gateway.WithEnableRawEvents(true),
		gateway.WithLogger(slog.Default()),
	)
	w.client = client
	return w, nil
}

// handleGatewayEvent runs on the gateway goroutine for every event. For each
// dispatch disgo first delivers the raw payload, then the parsed event: we
// keep the raw one, let disgo apply the parsed one to the cache, then
// publish the raw payload.
func (w *Watcher) handleGatewayEvent(gw gateway.Gateway, eventType gateway.EventType, seq int, event gateway.EventData) {
	if raw, ok := event.(gateway.EventRaw); ok {
		data, err := io.ReadAll(raw.Payload)
		if err != nil {
			slog.Error("watcher: read raw event", "type", raw.EventType, "err", err)
			return
		}
		w.raw, w.rawData = &raw, data
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// GUILD_CREATE: note which kind it is before disgo updates the flags.
	var kind queue.GuildCreateKind
	if gc, ok := event.(gateway.EventGuildCreate); ok {
		switch {
		case w.client.Caches.IsGuildUnready(gc.ID):
			kind = queue.GuildReady
		case w.client.Caches.IsGuildUnavailable(gc.ID):
			kind = queue.GuildAvailable
		default:
			kind = queue.GuildJoin
		}
	}

	w.client.EventManager.HandleGatewayEvent(gw, eventType, seq, event)

	if eventType == gateway.EventTypeReady {
		if r, ok := event.(gateway.EventReady); ok {
			slog.Info("watcher connected", "user", r.User.Username, "guilds", len(r.Guilds), "shard", gw.ShardID())
		}
	}

	if w.raw == nil || w.raw.EventType != eventType {
		return
	}
	data := w.rawData
	w.raw, w.rawData = nil, nil
	if !forwarded(eventType) {
		return
	}

	env := queue.Envelope{
		Type:        string(eventType),
		GuildID:     queue.GuildIDFromPayload(string(eventType), data),
		ShardID:     gw.ShardID(),
		Seq:         seq,
		ReceivedAt:  time.Now(),
		GuildCreate: kind,
		Data:        data,
	}
	if err := w.publisher.Publish(context.Background(), env); err != nil {
		slog.Error("watcher: publish event dropped", "type", eventType, "guild_id", env.GuildID, "err", err)
	}
}

// forwarded reports whether workers need an event type. Session events are
// the watcher's own business.
func forwarded(t gateway.EventType) bool {
	switch t {
	case gateway.EventTypeReady, gateway.EventTypeResumed:
		return false
	}
	return true
}

// resync publishes a snapshot (a synthetic GUILD_CREATE) of every guild in
// the partition, built from the cache, for a worker that just took it over.
func (w *Watcher) resync(partition int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var n int
	for guild := range w.client.Caches.Guilds() {
		if w.publisher.PartitionOf(guild.ID) != partition {
			continue
		}
		data, err := json.Marshal(w.snapshot(guild))
		if err != nil {
			slog.Error("watcher: marshal snapshot", "guild_id", guild.ID, "err", err)
			continue
		}
		if err := w.publisher.Publish(context.Background(), queue.Envelope{
			Type:        string(gateway.EventTypeGuildCreate),
			GuildID:     guild.ID,
			ReceivedAt:  time.Now(),
			GuildCreate: queue.GuildReady,
			Data:        data,
		}); err != nil {
			slog.Error("watcher: publish snapshot", "guild_id", guild.ID, "err", err)
			continue
		}
		n++
	}
	slog.Info("watcher: resynced partition", "partition", partition, "guilds", n)
}

// snapshot rebuilds a guild's GUILD_CREATE payload from the cache.
func (w *Watcher) snapshot(guild discord.Guild) discord.GatewayGuild {
	c := w.client.Caches
	g := discord.GatewayGuild{RestGuild: discord.RestGuild{Guild: guild}}
	for r := range c.Roles(guild.ID) {
		g.Roles = append(g.Roles, r)
	}
	for ch := range c.ChannelsForGuild(guild.ID) {
		if t, ok := ch.(discord.GuildThread); ok {
			g.Threads = append(g.Threads, t)
		} else {
			g.Channels = append(g.Channels, ch)
		}
	}
	for m := range c.Members(guild.ID) {
		g.Members = append(g.Members, m)
	}
	for vs := range c.VoiceStates(guild.ID) {
		g.VoiceStates = append(g.VoiceStates, vs)
	}
	return g
}

// Run connects to the gateway and forwards events until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	if err := w.client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}

	go w.publisher.HandleResyncRequests(ctx, w.resync)
	go w.keepAlive(ctx)

	<-ctx.Done()
	slog.Info("shutting down watcher")

	closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := w.publisher.ClearGatewayAlive(closeCtx); err != nil {
		slog.Warn("watcher: clear gateway alive", "err", err)
	}
	w.client.Close(closeCtx)
	return nil
}

// keepAlive refreshes the "gateway up" signal while the gateway is connected.
func (w *Watcher) keepAlive(ctx context.Context) {
	ticker := time.NewTicker(aliveInterval)
	defer ticker.Stop()
	for {
		if w.client.Gateway.Status() == gateway.StatusReady {
			if err := w.publisher.SetGatewayAlive(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("watcher: set gateway alive", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
