package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"
)

// gatewayAliveTTL is how long the watcher's "gateway up" signal lasts
// without being refreshed.
const gatewayAliveTTL = 30 * time.Second

// Publisher is the watcher's side of the queue.
type Publisher struct {
	rdb        *redis.Client
	partitions int
}

// NewPublisher returns a Publisher for the given number of partitions, and
// creates the workers consumer group on every partition so events published
// before any worker starts are kept for them.
func NewPublisher(ctx context.Context, rdb *redis.Client, partitions int) (*Publisher, error) {
	for p := range partitions {
		if err := ensureGroup(ctx, rdb, p); err != nil {
			return nil, fmt.Errorf("create group on partition %d: %w", p, err)
		}
	}
	return &Publisher{rdb: rdb, partitions: partitions}, nil
}

// Publish appends env to its guild's partition stream. It retries briefly
// if Redis is unavailable, then gives up (the event is dropped and logged).
func (p *Publisher) Publish(ctx context.Context, env Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	args := &redis.XAddArgs{
		Stream: eventsStream(Partition(env.GuildID, p.partitions)),
		MaxLen: streamMaxLen,
		Approx: true,
		Values: map[string]any{"e": b},
	}

	delay := 100 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err = p.rdb.XAdd(ctx, args).Err()
		if err == nil || attempt == 5 || ctx.Err() != nil {
			return err
		}
		slog.Warn("queue: publish failed, retrying", "type", env.Type, "attempt", attempt, "err", err)
		time.Sleep(delay)
		delay *= 2
	}
}

// Partitions returns how many partitions events are spread across.
func (p *Publisher) Partitions() int { return p.partitions }

// PartitionOf returns the partition a guild belongs to.
func (p *Publisher) PartitionOf(guildID snowflake.ID) int {
	return Partition(guildID, p.partitions)
}

// SetGatewayAlive refreshes the "gateway up" signal workers read.
func (p *Publisher) SetGatewayAlive(ctx context.Context) error {
	return p.rdb.Set(ctx, gatewayAliveKey, time.Now().Unix(), gatewayAliveTTL).Err()
}

// ClearGatewayAlive removes the "gateway up" signal (on shutdown).
func (p *Publisher) ClearGatewayAlive(ctx context.Context) error {
	return p.rdb.Del(ctx, gatewayAliveKey).Err()
}

// HandleResyncRequests blocks until ctx is done, calling resync for every
// partition a worker asks to be resynced (after taking it over, or on start).
func (p *Publisher) HandleResyncRequests(ctx context.Context, resync func(partition int)) {
	lastID := "$" // only requests made from now on
	for ctx.Err() == nil {
		res, err := p.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{controlStream, lastID},
			Block:   5 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("queue: read control stream", "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		for _, stream := range res {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				raw, _ := msg.Values["m"].(string)
				var cm controlMessage
				if err := json.Unmarshal([]byte(raw), &cm); err != nil {
					slog.Warn("queue: bad control message", "id", msg.ID, "err", err)
					continue
				}
				if cm.Op == opResync && cm.Partition >= 0 && cm.Partition < p.partitions {
					resync(cm.Partition)
				}
			}
		}
	}
}
