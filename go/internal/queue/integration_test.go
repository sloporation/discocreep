package queue

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/locks"
)

// TestQueueEndToEnd runs the publisher and two consumers against a real
// Redis/Valkey. Skipped unless BXT_TEST_REDIS_ADDR is set, e.g.:
//
//	docker run --rm -d -p 16379:6379 valkey/valkey:8
//	BXT_TEST_REDIS_ADDR=localhost:16379 go test ./internal/queue/ -run EndToEnd -v
//
// It uses database 14 and flushes it (the api tests use 15; packages run in parallel).
func TestQueueEndToEnd(t *testing.T) {
	addr := os.Getenv("BXT_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("BXT_TEST_REDIS_ADDR not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 14})
	defer rdb.Close()
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}

	const partitions = 4
	pub, err := NewPublisher(ctx, rdb, partitions)
	if err != nil {
		t.Fatal(err)
	}

	// Record every handled event: which worker, which guild, which seq.
	var mu sync.Mutex
	seen := map[snowflake.ID][]int{}
	byWorker := map[string]int{}
	handler := func(name string) Handler {
		return func(_ context.Context, env Envelope) error {
			mu.Lock()
			defer mu.Unlock()
			seen[env.GuildID] = append(seen[env.GuildID], env.Seq)
			byWorker[name]++
			return nil
		}
	}

	ctxA, stopA := context.WithCancel(ctx)
	a := NewConsumer(rdb, locks.New(rdb), partitions, handler("a"))
	go a.Run(ctxA)
	waitFor(t, "worker a to claim every partition", func() bool { return len(owned(a)) == partitions })

	// Each partition asked the watcher for a resync.
	if n, _ := rdb.XLen(ctx, controlStream).Result(); n < partitions {
		t.Errorf("control stream has %d resync requests, want >= %d", n, partitions)
	}

	// 8 guilds (spread over the 4 partitions), 25 events each, in order.
	guilds := []snowflake.ID{100, 101, 102, 103, 104, 105, 106, 107}
	publish := func(from, to int) {
		for seq := from; seq <= to; seq++ {
			for _, g := range guilds {
				if err := pub.Publish(ctx, Envelope{Type: "MESSAGE_CREATE", GuildID: g, Seq: seq, ReceivedAt: time.Now(), Data: json.RawMessage(`{}`)}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	publish(1, 25)
	waitFor(t, "first batch", func() bool { return total(&mu, seen) == len(guilds)*25 })

	// A second worker joins: partitions rebalance to 2 each.
	b := NewConsumer(rdb, locks.New(rdb), partitions, handler("b"))
	go b.Run(ctx)
	waitFor(t, "rebalance to 2+2", func() bool { return len(owned(a)) == 2 && len(owned(b)) == 2 })

	publish(26, 50)
	waitFor(t, "second batch", func() bool { return total(&mu, seen) == len(guilds)*50 })

	// Worker a stops: b takes every partition and keeps going.
	stopA()
	waitFor(t, "worker b to take over", func() bool { return len(owned(b)) == partitions })
	publish(51, 60)
	waitFor(t, "third batch", func() bool { return total(&mu, seen) == len(guilds)*60 })

	mu.Lock()
	defer mu.Unlock()
	for _, g := range guilds {
		got := seen[g]
		if len(got) != 60 {
			t.Errorf("guild %d: handled %d events, want 60 (exactly once each)", g, len(got))
			continue
		}
		for i, seq := range got {
			if seq != i+1 {
				t.Errorf("guild %d: event %d had seq %d; events out of order", g, i, seq)
				break
			}
		}
	}
	if byWorker["a"] == 0 || byWorker["b"] == 0 {
		t.Errorf("work wasn't shared: %v", byWorker)
	}
	t.Logf("events handled per worker: %v", byWorker)
}

func owned(c *Consumer) []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ps []int
	for p, op := range c.owned {
		select {
		case <-op.done:
		default:
			ps = append(ps, p)
		}
	}
	return ps
}

func total(mu *sync.Mutex, seen map[snowflake.ID][]int) int {
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, s := range seen {
		n += len(s)
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
