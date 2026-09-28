package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

const webOrigin = "http://localhost:5173"

func testServer(t *testing.T, rdb *redis.Client) *Server {
	t.Helper()
	if rdb == nil {
		// Unreachable Redis: fine for handlers that don't need it (errors are logged).
		rdb = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	}
	cfg := config.Config{
		Discord: config.DiscordConfig{ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:     config.APIConfig{PublicURL: "http://localhost:8080", WebURL: webOrigin},
	}
	s, err := New(cfg, rdb)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func do(t *testing.T, h http.Handler, method, path string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCORS(t *testing.T) {
	h := testServer(t, nil).Handler()

	rec := do(t, h, http.MethodOptions, "/api/me", map[string]string{"Origin": webOrigin})
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != webOrigin || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("preflight from web app: %d %v", rec.Code, rec.Header())
	}

	rec = do(t, h, http.MethodGet, "/api/me", map[string]string{"Origin": "https://evil.example"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("other origin got Access-Control-Allow-Origin %q", got)
	}
}

func TestWritesMustComeFromWebApp(t *testing.T) {
	h := testServer(t, nil).Handler()

	for _, origin := range []string{"", "https://evil.example"} {
		rec := do(t, h, http.MethodPost, "/auth/logout", map[string]string{"Origin": origin})
		if rec.Code != http.StatusForbidden {
			t.Errorf("logout from origin %q: %d, want 403", origin, rec.Code)
		}
	}
	rec := do(t, h, http.MethodPost, "/auth/logout", map[string]string{"Origin": webOrigin})
	if rec.Code != http.StatusNoContent {
		t.Errorf("logout from web app: %d, want 204", rec.Code)
	}
}

func TestLoginRedirectsToDiscord(t *testing.T) {
	h := testServer(t, nil).Handler()

	rec := do(t, h, http.MethodGet, "/auth/login", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("status %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := loc.Query()
	if loc.Host != "discord.com" || q.Get("client_id") != "1508701143583166575" ||
		q.Get("redirect_uri") != "http://localhost:8080/auth/callback" ||
		q.Get("response_type") != "code" || q.Get("scope") != "identify guilds" || q.Get("state") == "" {
		t.Errorf("unexpected authorize URL: %s", loc)
	}

	var stateCookieValue string
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookie {
			stateCookieValue = c.Value
			if !c.HttpOnly {
				t.Error("state cookie should be HttpOnly")
			}
		}
	}
	if stateCookieValue != q.Get("state") {
		t.Errorf("state cookie %q doesn't match state %q", stateCookieValue, q.Get("state"))
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	h := testServer(t, nil).Handler()

	cases := map[string]*http.Cookie{
		"no cookie":      nil,
		"cookie differs": {Name: stateCookie, Value: "other"},
	}
	for name, cookie := range cases {
		t.Run(name, func(t *testing.T) {
			var cookies []*http.Cookie
			if cookie != nil {
				cookies = append(cookies, cookie)
			}
			rec := do(t, h, http.MethodGet, "/auth/callback?code=abc&state=xyz", nil, cookies...)
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusFound || !strings.HasPrefix(loc, webOrigin+"/") || !strings.Contains(loc, "login_error=invalid_state") {
				t.Errorf("got %d %q, want redirect to web app with login_error=invalid_state", rec.Code, loc)
			}
		})
	}
}

func TestMeRequiresSession(t *testing.T) {
	h := testServer(t, nil).Handler()
	if rec := do(t, h, http.MethodGet, "/api/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

// TestSessionRoundTrip stores a session in a real Redis/Valkey and reads it
// back through /api/me. Skipped unless BXT_TEST_REDIS_ADDR is set; uses DB 15.
func TestSessionRoundTrip(t *testing.T) {
	addr := os.Getenv("BXT_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("BXT_TEST_REDIS_ADDR not set")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer rdb.Close()
	ctx := context.Background()
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	s := testServer(t, rdb)
	h := s.Handler()

	id, err := s.sessions.create(ctx, session{UserID: 42, Username: "alice", AvatarURL: "https://cdn/a.png"})
	if err != nil {
		t.Fatal(err)
	}
	// The raw ID isn't the Redis key.
	if n, _ := rdb.Exists(ctx, sessionKeyPrefix+id).Result(); n != 0 {
		t.Error("session stored under its raw ID; should be hashed")
	}

	rec := do(t, h, http.MethodGet, "/api/me", nil, &http.Cookie{Name: sessionCookie, Value: id})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"alice"`) {
		t.Fatalf("/api/me = %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodPost, "/auth/logout", map[string]string{"Origin": webOrigin}, &http.Cookie{Name: sessionCookie, Value: id})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/me", nil, &http.Cookie{Name: sessionCookie, Value: id}); rec.Code != http.StatusUnauthorized {
		t.Errorf("after logout /api/me = %d, want 401", rec.Code)
	}

	// States are single-use.
	state := s.oauth.StateController.NewState("http://localhost:8080/auth/callback")
	if s.oauth.StateController.UseState(state) == "" || s.oauth.StateController.UseState(state) != "" {
		t.Error("state should work exactly once")
	}
}
