// Package queue carries gateway events from the watcher to workers over
// Redis (Valkey) Streams.
//
// Layout:
//
//	bxt:events:{0..N-1}  one stream per partition; a guild's events always go
//	                     to partition guild_id % N, so they stay in order
//	bxt:control          workers → watcher requests (e.g. "resync partition 7")
//	bxt:gateway:alive    set by the watcher while its gateway is connected
//
// Each partition is consumed by exactly one worker at a time (see Consumer),
// which holds a lease on it. Interactions travel in the same streams as the
// rest of their guild's events, so the worker handling them has that guild's
// cache (voice states, channels, ...).
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

const (
	// group is the consumer group every worker reads partitions through.
	group = "workers"
	// controlStream carries requests from workers to the watcher.
	controlStream = "bxt:control"
	// gatewayAliveKey exists while the watcher's gateway is connected.
	gatewayAliveKey = "bxt:gateway:alive"
	// streamMaxLen bounds each partition stream; it's a transit buffer, not
	// storage, so old entries are trimmed (approximately) past this.
	streamMaxLen = 100_000
)

// eventsStream is the stream for a partition.
func eventsStream(partition int) string {
	return fmt.Sprintf("bxt:events:%d", partition)
}

// Partition returns the partition a guild's events go to. Events with no
// guild (DMs) go to partition 0.
func Partition(guildID snowflake.ID, partitions int) int {
	return int(uint64(guildID) % uint64(partitions))
}

// GuildCreateKind says which event a GUILD_CREATE should produce on the
// worker, mirroring what the watcher's own gateway saw.
type GuildCreateKind string

const (
	// GuildReady: part of the initial connect (or a resync snapshot).
	GuildReady GuildCreateKind = "ready"
	// GuildAvailable: the guild came back after an outage.
	GuildAvailable GuildCreateKind = "available"
	// GuildJoin: the bot was added to the guild.
	GuildJoin GuildCreateKind = "join"
)

// Envelope is one gateway dispatch as published by the watcher.
type Envelope struct {
	// Type is the gateway event name, e.g. "MESSAGE_CREATE".
	Type string `json:"t"`
	// GuildID is the guild the event belongs to, 0 if none.
	GuildID snowflake.ID `json:"g,omitempty"`
	// ShardID and Seq are the watcher's gateway shard and sequence number.
	ShardID int `json:"shard"`
	Seq     int `json:"s"`
	// ReceivedAt is when the watcher received the event. For interactions,
	// Discord's 3 second reply deadline counts from about this moment.
	ReceivedAt time.Time `json:"at"`
	// GuildCreate is set for GUILD_CREATE events.
	GuildCreate GuildCreateKind `json:"gc,omitempty"`
	// Data is the raw event payload ("d"), exactly as Discord sent it.
	Data json.RawMessage `json:"d"`
}

// GuildIDFromPayload extracts the guild an event belongs to from its raw
// payload: "id" for GUILD_CREATE/UPDATE/DELETE, "guild_id" for everything else.
func GuildIDFromPayload(eventType string, data []byte) snowflake.ID {
	var v struct {
		ID      snowflake.ID  `json:"id"`
		GuildID *snowflake.ID `json:"guild_id"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return 0
	}
	switch eventType {
	case "GUILD_CREATE", "GUILD_UPDATE", "GUILD_DELETE":
		return v.ID
	}
	if v.GuildID != nil {
		return *v.GuildID
	}
	return 0
}

// controlMessage is a request from a worker to the watcher.
type controlMessage struct {
	Op        string `json:"op"`
	Partition int    `json:"partition"`
}

const opResync = "resync"

// NewRedis connects to Redis/Valkey and checks the connection.
func NewRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("ping %s: %w", cfg.Addr, err)
	}
	return rdb, nil
}

// ensureGroup creates the workers consumer group on a partition stream if it
// doesn't exist. New groups start at the end of the stream ("$"): workers
// rebuild state from a resync snapshot, not by replaying old history.
func ensureGroup(ctx context.Context, rdb *redis.Client, partition int) error {
	err := rdb.XGroupCreateMkStream(ctx, eventsStream(partition), group, "$").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// GatewayStatus reports whether the watcher's gateway is currently
// connected, as seen from a worker.
type GatewayStatus struct {
	rdb *redis.Client
}

// NewGatewayStatus returns a GatewayStatus reading from rdb.
func NewGatewayStatus(rdb *redis.Client) *GatewayStatus {
	return &GatewayStatus{rdb: rdb}
}

// Up reports whether the watcher has recently confirmed its gateway is connected.
func (g *GatewayStatus) Up(ctx context.Context) bool {
	n, err := g.rdb.Exists(ctx, gatewayAliveKey).Result()
	return err == nil && n > 0
}
