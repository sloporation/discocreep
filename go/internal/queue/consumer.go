package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/locks"
)

const (
	// partitionLeaseTTL is how long a partition stays claimed if its worker
	// stops renewing (i.e. dies) before another worker can take it.
	partitionLeaseTTL = 15 * time.Second
	// workerTTL is how long a worker counts as alive without a heartbeat.
	workerTTL = 15 * time.Second
	// rebalanceInterval is how often a worker heartbeats and rebalances.
	rebalanceInterval = 5 * time.Second
	// workerKeyPrefix marks live workers (used to share partitions fairly).
	workerKeyPrefix = "bxt:worker:"
)

// Handler processes one event. Returning an error only logs it: the entry
// is acknowledged either way, so a bad event can't block its partition.
type Handler func(ctx context.Context, env Envelope) error

// Consumer is a worker's side of the queue. It claims a fair share of the
// partitions (by lease), processes each claimed partition's events in order,
// and hands partitions back when it has more than its share.
type Consumer struct {
	rdb        *redis.Client
	locker     *locks.Locker
	partitions int
	handle     Handler
	id         string

	mu    sync.Mutex
	owned map[int]*ownedPartition
}

type ownedPartition struct {
	lease  *locks.Lease
	cancel context.CancelFunc
	done   chan struct{}
}

// NewConsumer returns a Consumer that calls handle for every event.
func NewConsumer(rdb *redis.Client, locker *locks.Locker, partitions int, handle Handler) *Consumer {
	return &Consumer{
		rdb:        rdb,
		locker:     locker,
		partitions: partitions,
		handle:     handle,
		id:         workerID(),
		owned:      make(map[int]*ownedPartition),
	}
}

// ID is this worker's consumer name.
func (c *Consumer) ID() string { return c.id }

// Run heartbeats, rebalances and processes partitions until ctx is done,
// then releases everything so other workers can take over immediately.
func (c *Consumer) Run(ctx context.Context) {
	slog.Info("queue: worker starting", "worker", c.id, "partitions", c.partitions)
	ticker := time.NewTicker(rebalanceInterval)
	defer ticker.Stop()

	for {
		c.rebalance(ctx)
		select {
		case <-ctx.Done():
			c.releaseAll()
			c.rdb.Del(context.Background(), workerKeyPrefix+c.id)
			slog.Info("queue: worker stopped", "worker", c.id)
			return
		case <-ticker.C:
		}
	}
}

// rebalance refreshes this worker's heartbeat, drops partitions whose lease
// was lost or that exceed its fair share, and claims free ones up to it.
func (c *Consumer) rebalance(ctx context.Context) {
	if err := c.rdb.Set(ctx, workerKeyPrefix+c.id, time.Now().Unix(), workerTTL).Err(); err != nil {
		slog.Warn("queue: worker heartbeat", "err", err)
	}

	live, err := c.liveWorkers(ctx)
	if err != nil || live < 1 {
		live = 1
	}
	share := (c.partitions + live - 1) / live // ceil

	c.mu.Lock()
	var owned []int
	for p, op := range c.owned {
		select {
		case <-op.done: // stopped (lease lost)
			delete(c.owned, p)
		default:
			owned = append(owned, p)
		}
	}
	c.mu.Unlock()

	// Hand back the excess, highest partitions first.
	sort.Sort(sort.Reverse(sort.IntSlice(owned)))
	for len(owned) > share {
		c.release(owned[0])
		owned = owned[1:]
	}

	// Claim free partitions up to our share.
	for p := 0; p < c.partitions && len(owned) < share; p++ {
		if c.isOwned(p) {
			continue
		}
		lease, ok, err := c.locker.Acquire(ctx, fmt.Sprintf("partition:%d", p), partitionLeaseTTL)
		if err != nil {
			slog.Warn("queue: claim partition", "partition", p, "err", err)
			continue
		}
		if !ok {
			continue
		}
		c.start(ctx, p, lease)
		owned = append(owned, p)
	}
}

func (c *Consumer) liveWorkers(ctx context.Context) (int, error) {
	var n int
	iter := c.rdb.Scan(ctx, 0, workerKeyPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		n++
	}
	return n, iter.Err()
}

func (c *Consumer) isOwned(p int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.owned[p]
	return ok
}

// start processes partition p until its lease is lost or it's released.
func (c *Consumer) start(parent context.Context, p int, lease *locks.Lease) {
	ctx, cancel := context.WithCancel(parent)
	op := &ownedPartition{lease: lease, cancel: cancel, done: make(chan struct{})}
	c.mu.Lock()
	c.owned[p] = op
	c.mu.Unlock()

	go func() {
		select {
		case <-lease.Lost():
			slog.Warn("queue: lost partition lease", "partition", p)
			cancel()
		case <-ctx.Done():
		}
	}()
	go func() {
		defer close(op.done)
		c.consume(ctx, p)
	}()
	slog.Info("queue: claimed partition", "partition", p, "worker", c.id)
}

// release stops processing p and gives up its lease.
func (c *Consumer) release(p int) {
	c.mu.Lock()
	op, ok := c.owned[p]
	delete(c.owned, p)
	c.mu.Unlock()
	if !ok {
		return
	}
	op.cancel()
	<-op.done
	op.lease.Release()
	slog.Info("queue: released partition", "partition", p, "worker", c.id)
}

func (c *Consumer) releaseAll() {
	c.mu.Lock()
	ps := make([]int, 0, len(c.owned))
	for p := range c.owned {
		ps = append(ps, p)
	}
	c.mu.Unlock()
	for _, p := range ps {
		c.release(p)
	}
}

// consume processes partition p: first anything the previous owner read
// but didn't acknowledge, then new events, after asking the watcher for a
// snapshot of the partition's guilds so this worker's cache is complete.
func (c *Consumer) consume(ctx context.Context, p int) {
	stream := eventsStream(p)
	if err := ensureGroup(ctx, c.rdb, p); err != nil {
		slog.Error("queue: create group", "partition", p, "err", err)
		return
	}
	if err := c.requestResync(ctx, p); err != nil {
		slog.Warn("queue: request resync", "partition", p, "err", err)
	}

	// Take over entries the previous owner had in flight. We hold the
	// partition's lease, so nobody else is working on them.
	start := "0-0"
	for ctx.Err() == nil {
		msgs, next, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   stream,
			Group:    group,
			Consumer: c.id,
			Start:    start,
			Count:    100,
		}).Result()
		if err != nil {
			slog.Warn("queue: claim pending", "partition", p, "err", err)
			break
		}
		c.process(ctx, stream, msgs)
		if next == "0-0" || len(msgs) == 0 {
			break
		}
		start = next
	}

	for ctx.Err() == nil {
		res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: c.id,
			Streams:  []string{stream, ">"},
			Count:    100,
			Block:    2 * time.Second,
		}).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("queue: read partition", "partition", p, "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		for _, s := range res {
			c.process(ctx, stream, s.Messages)
		}
	}
}

// process handles entries in order and acknowledges each.
func (c *Consumer) process(ctx context.Context, stream string, msgs []redis.XMessage) {
	for _, msg := range msgs {
		raw, _ := msg.Values["e"].(string)
		var env Envelope
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			slog.Error("queue: bad envelope", "stream", stream, "id", msg.ID, "err", err)
		} else if err := c.handle(ctx, env); err != nil {
			slog.Error("queue: handle event", "type", env.Type, "guild_id", env.GuildID, "err", err)
		}
		// Ack with a fresh context so a shutdown mid-batch still acks what was handled.
		if err := c.rdb.XAck(context.Background(), stream, group, msg.ID).Err(); err != nil {
			slog.Warn("queue: ack", "stream", stream, "id", msg.ID, "err", err)
		}
	}
}

func (c *Consumer) requestResync(ctx context.Context, p int) error {
	b, _ := json.Marshal(controlMessage{Op: opResync, Partition: p})
	return c.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: controlStream,
		MaxLen: 1000,
		Approx: true,
		Values: map[string]any{"m": b},
	}).Err()
}

// workerID is a unique consumer name for this process.
func workerID() string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))
}
