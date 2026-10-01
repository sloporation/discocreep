package api

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Per-client request limits, counted in Redis so they hold across API
// instances. /auth/* is tight: each login stores state in Redis and sends
// the user to Discord, so it's what a flood would target. /api/* is loose
// enough for a busy dashboard (a page load makes several calls).
var rateLimits = []struct {
	prefix string
	bucket string
	limit  int
}{
	{"/auth/", "auth", 30},
	{"/api/", "api", 600},
}

// rateWindow is the length of each counting window.
const (
	rateWindow       = time.Minute
	rateKeyPrefix    = "bxt:ratelimit:"
	maxClientKeySize = 64
)

// rateLimiter limits requests per client IP with fixed one-minute windows.
type rateLimiter struct {
	rdb      *redis.Client
	ipHeader string // header the proxy puts the client IP in; "" = connection address
	now      func() time.Time
	// onLimitedNavigation, if set, answers a limited GET /auth/* request.
	// Those are page loads (login, linking), not API calls, so the user is
	// better sent back to the web app with a message than shown JSON.
	onLimitedNavigation func(w http.ResponseWriter, r *http.Request)

	warnOnce sync.Once
}

func newRateLimiter(rdb *redis.Client, ipHeader string) *rateLimiter {
	return &rateLimiter{rdb: rdb, ipHeader: strings.TrimSpace(ipHeader), now: time.Now}
}

// middleware answers 429 (with Retry-After) once a client goes over its
// limit. If Redis can't be reached it lets requests through: the API is
// failing anyway, and locking everyone out wouldn't help.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket, limit := "", 0
		for _, l := range rateLimits {
			if strings.HasPrefix(r.URL.Path, l.prefix) {
				bucket, limit = l.bucket, l.limit
				break
			}
		}
		if bucket == "" || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		now := rl.now()
		window := now.Truncate(rateWindow)
		key := fmt.Sprintf("%s%s:%s:%d", rateKeyPrefix, bucket, rl.clientIP(r), window.Unix())
		pipe := rl.rdb.TxPipeline()
		count := pipe.Incr(r.Context(), key)
		pipe.Expire(r.Context(), key, rateWindow+5*time.Second)
		if _, err := pipe.Exec(r.Context()); err != nil {
			slog.Warn("api: rate limit check failed; allowing request", "err", err)
			next.ServeHTTP(w, r)
			return
		}
		if count.Val() > int64(limit) {
			retry := int(window.Add(rateWindow).Sub(now).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			if bucket == "auth" && r.Method == http.MethodGet && rl.onLimitedNavigation != nil {
				rl.onLimitedNavigation(w, r)
				return
			}
			writeError(w, http.StatusTooManyRequests, "rate_limited")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the IP requests are counted against.
func (rl *rateLimiter) clientIP(r *http.Request) string {
	if rl.ipHeader != "" {
		if v := r.Header.Get(rl.ipHeader); v != "" {
			// X-Forwarded-For is a list; the last entry is the one the
			// nearest proxy (ours) added. Earlier ones are client-supplied.
			if i := strings.LastIndex(v, ","); i >= 0 {
				v = v[i+1:]
			}
			if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil {
				return ip.String()
			}
		}
	} else if r.Header.Get("X-Forwarded-For") != "" {
		rl.warnOnce.Do(func() {
			slog.Warn("api: requests arrive through a proxy (X-Forwarded-For is set) but api.client_ip_header (BXT_API_CLIENT_IP_HEADER) isn't, so all users share one rate limit. Set it to the header your proxy puts the client IP in.")
		})
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(host) > maxClientKeySize {
		host = host[:maxClientKeySize]
	}
	return host
}
