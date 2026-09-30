package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

func TestSecurityHeaders(t *testing.T) {
	h := testServer(t, nil).Handler()
	rec := do(t, h, http.MethodGet, "/api/me", nil)
	for k, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS sent over http")
	}
	// A 401 still reaches the web app with CORS headers.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/me = %d", rec.Code)
	}

	cfg := config.Config{
		Discord: config.DiscordConfig{Token: "bot-token", ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:     config.APIConfig{PublicURL: "https://api.example.com", WebURL: "https://app.example.com"},
	}
	s, err := New(cfg, nil, redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1}))
	if err != nil {
		t.Fatal(err)
	}
	if got := do(t, s.Handler(), http.MethodGet, "/healthz", nil).Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("no HSTS over https")
	}
}

func TestClientIP(t *testing.T) {
	req := func(remote string, headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	direct := newRateLimiter(nil, "")
	if got := direct.clientIP(req("203.0.113.9:5555", nil)); got != "203.0.113.9" {
		t.Errorf("direct = %q", got)
	}
	// Without a configured header, a spoofed X-Forwarded-For is ignored.
	if got := direct.clientIP(req("203.0.113.9:5555", map[string]string{"X-Forwarded-For": "1.1.1.1"})); got != "203.0.113.9" {
		t.Errorf("unconfigured XFF = %q", got)
	}
	xff := newRateLimiter(nil, "X-Forwarded-For")
	// The last entry is the one our proxy added; earlier ones are the client's.
	if got := xff.clientIP(req("10.0.0.2:1", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.7"})); got != "198.51.100.7" {
		t.Errorf("XFF = %q", got)
	}
	// A header that isn't an IP falls back to the connection address.
	if got := xff.clientIP(req("10.0.0.2:1", map[string]string{"X-Forwarded-For": "not-an-ip"})); got != "10.0.0.2" {
		t.Errorf("bad XFF = %q", got)
	}
	cf := newRateLimiter(nil, "CF-Connecting-IP")
	if got := cf.clientIP(req("10.0.0.2:1", map[string]string{"CF-Connecting-IP": "2001:db8::1"})); got != "2001:db8::1" {
		t.Errorf("CF = %q", got)
	}
}

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("BXT_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("BXT_TEST_REDIS_ADDR not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return rdb
}

// TestRateLimit needs Redis/Valkey (BXT_TEST_REDIS_ADDR; DB 15).
func TestRateLimit(t *testing.T) {
	rdb := testRedis(t)
	rl := newRateLimiter(rdb, "")
	now := time.Date(2026, 9, 30, 12, 0, 10, 0, time.UTC)
	rl.now = func() time.Time { return now }
	h := rl.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	hit := func(path, ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	for i := 0; i < 30; i++ {
		if rec := hit("/auth/login", "203.0.113.1"); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i+1, rec.Code)
		}
	}
	rec := hit("/auth/login", "203.0.113.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "51" {
		t.Errorf("31st = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Page loads under /auth/ go back to the web app with a message instead.
	rl.onLimitedNavigation = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, webOrigin+"/?login_error=rate_limited", http.StatusFound)
	}
	if rec := hit("/auth/login", "203.0.113.1"); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "login_error=rate_limited") {
		t.Errorf("limited navigation = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rl.onLimitedNavigation = nil
	// Other clients, other buckets and unlimited paths are unaffected.
	if rec := hit("/auth/login", "203.0.113.2"); rec.Code != http.StatusOK {
		t.Errorf("other client = %d", rec.Code)
	}
	if rec := hit("/api/me", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Errorf("api bucket = %d", rec.Code)
	}
	if rec := hit("/healthz", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
	// The next window starts afresh.
	now = now.Add(time.Minute)
	if rec := hit("/auth/login", "203.0.113.1"); rec.Code != http.StatusOK {
		t.Errorf("next window = %d", rec.Code)
	}
}

// TestSessionEncryption needs Redis/Valkey (BXT_TEST_REDIS_ADDR; DB 15).
func TestSessionEncryption(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	st, err := newSessionStore(rdb, "secret")
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.create(ctx, session{UserID: 42, Username: "alice", OAuth: oauth2.Session{AccessToken: "discord-access-token", RefreshToken: "discord-refresh-token"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := rdb.Get(ctx, sessionKey(id)).Bytes()
	if strings.Contains(string(raw), "discord-access-token") || strings.Contains(string(raw), "alice") {
		t.Error("session stored in plain text")
	}
	if s, ok, err := st.get(ctx, id); err != nil || !ok || s.OAuth.AccessToken != "discord-access-token" {
		t.Fatalf("get = %+v %v %v", s, ok, err)
	}

	// A value moved to another session's key doesn't decrypt there.
	other, _ := st.create(ctx, session{UserID: 43})
	rdb.Set(ctx, sessionKey(other), raw, time.Minute)
	if _, ok, _ := st.get(ctx, other); ok {
		t.Error("a session copied to another key was accepted")
	}

	// Another client secret can't read it (and drops it: that user is logged out).
	rotated, _ := newSessionStore(rdb, "new-secret")
	if _, ok, _ := rotated.get(ctx, id); ok {
		t.Error("session readable with a different secret")
	}
	if n, _ := rdb.Exists(ctx, sessionKey(id)).Result(); n != 0 {
		t.Error("unreadable session wasn't deleted")
	}

	// A session written before encryption (plain JSON) counts as logged out.
	legacy := randomToken(32)
	rdb.Set(ctx, sessionKey(legacy), `{"user_id":"42","username":"alice"}`, time.Minute)
	if _, ok, err := st.get(ctx, legacy); ok || err != nil {
		t.Errorf("legacy session: ok %v err %v", ok, err)
	}
}

// TestLogoutRevokesAndLogoutAll needs Redis/Valkey (BXT_TEST_REDIS_ADDR; DB 15).
func TestLogoutRevokesAndLogoutAll(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()

	var mu sync.Mutex
	revoked := map[string]bool{}
	discordAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if r.URL.Path != "/oauth2/token/revoke" || user != "1508701143583166575" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		mu.Lock()
		revoked[r.PostForm.Get("token")] = true
		mu.Unlock()
	}))
	defer discordAPI.Close()
	waitRevoked := func(tokens ...string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			all := true
			for _, tok := range tokens {
				all = all && revoked[tok]
			}
			mu.Unlock()
			if all {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("tokens not revoked: %v (revoked %v)", tokens, revoked)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	cfg := config.Config{
		Discord: config.DiscordConfig{Token: "bot-token", ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:     config.APIConfig{PublicURL: "http://localhost:8080", WebURL: webOrigin},
	}
	s, err := New(cfg, nil, rdb, withDiscordURL(discordAPI.URL))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	newSession := func(user int, token string) *http.Cookie {
		t.Helper()
		id, err := s.sessions.create(ctx, session{UserID: snowflakeID(user), OAuth: oauth2.Session{AccessToken: token, RefreshToken: token + "-refresh", Expiration: time.Now().Add(time.Hour)}})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: sessionCookie, Value: id}
	}
	origin := map[string]string{"Origin": webOrigin}

	// Logging out revokes that session's tokens.
	c := newSession(42, "phone")
	if rec := do(t, h, http.MethodPost, "/auth/logout", origin, c); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	waitRevoked("phone", "phone-refresh")

	// Log out everywhere: every one of the user's sessions ends, others' don't.
	laptop, desktop, bob := newSession(42, "laptop"), newSession(42, "desktop"), newSession(43, "bob")
	if rec := do(t, h, http.MethodPost, "/auth/logout-all", nil, laptop); rec.Code != http.StatusForbidden {
		t.Errorf("logout-all from another origin = %d, want 403", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/auth/logout-all", origin, laptop); rec.Code != http.StatusNoContent {
		t.Fatalf("logout-all = %d", rec.Code)
	}
	for name, c := range map[string]*http.Cookie{"laptop": laptop, "desktop": desktop} {
		if rec := do(t, h, http.MethodGet, "/api/me", nil, c); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s after logout-all = %d, want 401", name, rec.Code)
		}
	}
	if rec := do(t, h, http.MethodGet, "/api/me", nil, bob); rec.Code != http.StatusOK {
		t.Errorf("another user's session after logout-all = %d, want 200", rec.Code)
	}
	waitRevoked("laptop", "laptop-refresh", "desktop", "desktop-refresh")
	mu.Lock()
	if revoked["bob"] {
		t.Error("another user's token was revoked")
	}
	mu.Unlock()
	if rec := do(t, h, http.MethodPost, "/auth/logout-all", origin); rec.Code != http.StatusUnauthorized {
		t.Errorf("logout-all without a session = %d, want 401", rec.Code)
	}
}

func snowflakeID(n int) snowflake.ID { return snowflake.ID(n) }
