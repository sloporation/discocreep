package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

// fakeDiscord answers the Discord API calls the guild endpoints make.
//
//	user "ok":      in guilds 1 (Administrator), 2 (no perms), 3 (owner), 9 (no bot)
//	user "expired": Discord rejects the token
//	bot:            in guilds 1, 2, 3, 4
func fakeDiscord(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/users/@me/guilds" && auth == "Bearer ok":
			_, _ = w.Write([]byte(`[
				{"id":"1","name":"Admins","permissions":"8"},
				{"id":"2","name":"Members","permissions":"0"},
				{"id":"3","name":"Owned","owner":true,"permissions":"0"},
				{"id":"9","name":"No bot","permissions":"8"}]`))
		case r.URL.Path == "/users/@me/guilds" && auth == "Bearer expired":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401: Unauthorized","code":0}`))
		case r.URL.Path == "/users/@me/guilds" && auth == "Bot bot-token":
			_, _ = w.Write([]byte(`[{"id":"1","name":"a"},{"id":"2","name":"b"},{"id":"3","name":"c"},{"id":"4","name":"d"}]`))
		case r.URL.Path == "/guilds/1/channels" && auth == "Bot bot-token":
			_, _ = w.Write([]byte(`[
				{"id":"11","type":0,"guild_id":"1","name":"general","position":1},
				{"id":"12","type":2,"guild_id":"1","name":"Voice","position":2},
				{"id":"13","type":0,"guild_id":"1","name":"alerts","position":0}]`))
		default:
			t.Errorf("unexpected Discord call: %s %s (%s)", r.Method, r.URL.Path, auth)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestGuildAuthorization checks who sees which guilds and settings. Needs a
// real Redis/Valkey for sessions: skipped unless BXT_TEST_REDIS_ADDR is set.
func TestGuildAuthorization(t *testing.T) {
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

	discordAPI := fakeDiscord(t)
	defer discordAPI.Close()

	cfg := config.Config{
		Discord: config.DiscordConfig{Token: "bot-token", ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:     config.APIConfig{PublicURL: "http://localhost:8080", WebURL: webOrigin},
	}
	s, err := New(cfg, nil, rdb, withDiscordURL(discordAPI.URL))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	login := func(token string) *http.Cookie {
		id, err := s.sessions.create(ctx, session{
			UserID:   42,
			Username: "alice",
			OAuth:    oauth2.Session{AccessToken: token, Scopes: scopes, Expiration: time.Now().Add(time.Hour)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: sessionCookie, Value: id}
	}
	user := login("ok")

	// Shared guilds only (not 4: user isn't in it; not 9: bot isn't), sorted, with admin flags.
	rec := do(t, h, http.MethodGet, "/api/guilds", nil, user)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/guilds = %d %s", rec.Code, rec.Body.String())
	}
	var guilds []guildView
	if err := json.Unmarshal(rec.Body.Bytes(), &guilds); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	var order []string
	for _, g := range guilds {
		got[g.ID.String()] = g.IsAdmin
		order = append(order, g.Name)
	}
	want := map[string]bool{"1": true, "2": false, "3": true}
	if len(got) != len(want) {
		t.Fatalf("guilds = %v, want %v", got, want)
	}
	for id, admin := range want {
		if a, ok := got[id]; !ok || a != admin {
			t.Errorf("guild %s: present=%v admin=%v, want admin=%v", id, ok, a, admin)
		}
	}
	if strings.Join(order, ",") != "Admins,Members,Owned" {
		t.Errorf("order = %v, want sorted by name", order)
	}

	cases := []struct {
		path string
		want int
	}{
		{"/api/guilds/1", http.StatusOK},
		{"/api/guilds/2", http.StatusOK},                 // members can see the guild page
		{"/api/guilds/9", http.StatusNotFound},           // bot not in it
		{"/api/guilds/4", http.StatusNotFound},           // user not in it
		{"/api/guilds/nope", http.StatusNotFound},        // not an ID
		{"/api/guilds/2/settings", http.StatusForbidden}, // not an admin
		{"/api/guilds/2/channels", http.StatusForbidden},
		{"/api/guilds/1/channels", http.StatusOK},
	}
	for _, c := range cases {
		if rec := do(t, h, http.MethodGet, c.path, nil, user); rec.Code != c.want {
			t.Errorf("GET %s = %d, want %d (%s)", c.path, rec.Code, c.want, rec.Body.String())
		}
	}

	// Writes are admin-only too.
	rec = do(t, h, http.MethodPut, "/api/guilds/2/settings/purge", map[string]string{"Origin": webOrigin}, user)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT purge as non-admin = %d, want 403", rec.Code)
	}

	// Channel list: text channels only, in Discord's order.
	rec = do(t, h, http.MethodGet, "/api/guilds/1/channels", nil, user)
	if body := rec.Body.String(); !strings.Contains(body, `"alerts"`) || strings.Contains(body, "Voice") ||
		strings.Index(body, `"alerts"`) > strings.Index(body, `"general"`) {
		t.Errorf("channels = %s", body)
	}

	// Not logged in.
	if rec := do(t, h, http.MethodGet, "/api/guilds", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d, want 401", rec.Code)
	}

	// A session without the guilds scope must log in again.
	noScope, err := s.sessions.create(ctx, session{UserID: 42, OAuth: oauth2.Session{AccessToken: "ok", Expiration: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodGet, "/api/guilds", nil, &http.Cookie{Name: sessionCookie, Value: noScope}); rec.Code != http.StatusUnauthorized {
		t.Errorf("session without guilds scope = %d, want 401", rec.Code)
	}

	// Discord rejects the token: 401 and the session is gone.
	expired := login("expired")
	if rec := do(t, h, http.MethodGet, "/api/guilds", nil, expired); rec.Code != http.StatusUnauthorized {
		t.Errorf("expired token = %d, want 401", rec.Code)
	}
	if _, ok, _ := s.sessions.get(ctx, expired.Value); ok {
		t.Error("session should be deleted after Discord rejects its token")
	}
}
