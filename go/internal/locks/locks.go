// Package locks provides Redis-backed leases, so that when several worker
// processes run, a piece of work (a queue partition, a purge job, command
// sync) is done by exactly one of them.
//
// A lease expires on its own if its holder dies, so work is never stuck;
// while held it renews itself in the background. Features that need to
// coordinate across workers get a *Locker as bot.Locks. This package must
// not import internal/discord or any feature package.
package locks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyPrefix namespaces every lock key.
const keyPrefix = "bxt:lock:"

// renew extends the lease only if we still hold it.
var renew = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0`)

// release deletes the lease only if we still hold it.
var release = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0`)

// Locker hands out leases.
type Locker struct {
	rdb *redis.Client
}

// New returns a Locker using rdb.
func New(rdb *redis.Client) *Locker {
	return &Locker{rdb: rdb}
}

// Lease is a held lock. It renews itself every ttl/3 until Release is
// called or a renewal fails, at which point Lost is closed.
type Lease struct {
	rdb   *redis.Client
	key   string
	token string

	stop     chan struct{}
	lost     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// Acquire tries to take the lock named key for ttl. It returns ok = false
// (and no error) if someone else holds it.
func (l *Locker) Acquire(ctx context.Context, key string, ttl time.Duration) (lease *Lease, ok bool, err error) {
	token, err := randomToken()
	if err != nil {
		return nil, false, err
	}
	full := keyPrefix + key
	ok, err = l.rdb.SetNX(ctx, full, token, ttl).Result()
	if err != nil || !ok {
		return nil, false, err
	}

	lease = &Lease{
		rdb:   l.rdb,
		key:   full,
		token: token,
		stop:  make(chan struct{}),
		lost:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go lease.keepAlive(ttl)
	return lease, true, nil
}

// Once sets a marker that expires after ttl and reports whether this call
// set it (i.e. nobody else did within ttl). Use it for "do this once per
// period" work that doesn't need to be held, like command sync.
func (l *Locker) Once(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return l.rdb.SetNX(ctx, keyPrefix+key, "1", ttl).Result()
}

// Lost is closed if the lease could not be renewed and may now be held by
// someone else. Stop the protected work when it closes.
func (le *Lease) Lost() <-chan struct{} { return le.lost }

// Release gives up the lease and stops renewing it.
func (le *Lease) Release() {
	le.stopOnce.Do(func() { close(le.stop) })
	<-le.done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := release.Run(ctx, le.rdb, []string{le.key}, le.token).Err(); err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("locks: release", "key", le.key, "err", err)
	}
}

func (le *Lease) keepAlive(ttl time.Duration) {
	defer close(le.done)
	ticker := time.NewTicker(ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-le.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), ttl/3)
			n, err := renew.Run(ctx, le.rdb, []string{le.key}, le.token, ttl.Milliseconds()).Int()
			cancel()
			if err != nil || n == 0 {
				slog.Warn("locks: lease lost", "key", le.key, "err", err)
				close(le.lost)
				return
			}
		}
	}
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
