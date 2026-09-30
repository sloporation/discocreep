package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"/guilds/123":             "/guilds/123",
		"/":                       "/",
		"":                        "/",
		"guilds/123":              "/",
		"//evil.example/x":        "/",
		"https://evil.example/x":  "/",
		"/\\evil.example":         "/",
		"/guilds/1\r\nSet-Cookie": "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidAssertion(t *testing.T) {
	if !isValidAssertion("ns:http://specs.openid.net/auth/2.0\nis_valid:true\n") {
		t.Error("is_valid:true not accepted")
	}
	for _, body := range []string{"ns:x\nis_valid:false\n", "", "is_valid:truex", "<html>is_valid:true</html>"} {
		if isValidAssertion(body) {
			t.Errorf("%q accepted", body)
		}
	}
}

// memSteamStore is an in-memory steamStore.
type memSteamStore struct {
	mu    sync.Mutex
	links map[snowflake.ID]uint64
}

func (m *memSteamStore) get(_ context.Context, id snowflake.ID) (*steamLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid, ok := m.links[id]; ok {
		return &steamLink{SteamID: sid, LinkedAt: time.Now()}, nil
	}
	return nil, nil
}

func (m *memSteamStore) link(_ context.Context, id snowflake.ID, sid uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[id] = sid
	return nil
}

func (m *memSteamStore) unlink(_ context.Context, id snowflake.ID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.links[id]
	delete(m.links, id)
	return ok, nil
}

// TestSteamLinkFlow runs "Sign in through Steam" end to end against a fake
// Steam. Needs a real Redis/Valkey: skipped unless BXT_TEST_REDIS_ADDR is set.
func TestSteamLinkFlow(t *testing.T) {
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

	// Fake Steam: confirms only responses whose sig is "good".
	fakeSteam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Method != http.MethodPost || r.Form.Get("openid.mode") != "check_authentication" {
			t.Errorf("unexpected Steam call: %s %s", r.Method, r.URL)
		}
		if r.Form.Get("openid.sig") == "good" {
			_, _ = w.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:true\n"))
		} else {
			_, _ = w.Write([]byte("ns:http://specs.openid.net/auth/2.0\nis_valid:false\n"))
		}
	}))
	defer fakeSteam.Close()

	store := &memSteamStore{links: map[snowflake.ID]uint64{}}
	cfg := config.Config{
		Discord: config.DiscordConfig{Token: "bot-token", ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:     config.APIConfig{PublicURL: "http://localhost:8080", WebURL: webOrigin},
	}
	s, err := New(cfg, nil, rdb, withSteam(fakeSteam.URL, "", store))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	sessionID, err := s.sessions.create(ctx, session{UserID: 42, Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	discordCookie := &http.Cookie{Name: sessionCookie, Value: sessionID}

	// start begins a link and returns the state and its cookie.
	start := func() (string, *http.Cookie) {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/auth/steam/link?next=/guilds/7", nil, discordCookie)
		if rec.Code != http.StatusFound {
			t.Fatalf("link = %d", rec.Code)
		}
		loc, _ := url.Parse(rec.Header().Get("Location"))
		rt, _ := url.Parse(loc.Query().Get("openid.return_to"))
		state := rt.Query().Get("state")
		if !strings.HasPrefix(loc.String(), fakeSteam.URL) || loc.Query().Get("openid.mode") != "checkid_setup" ||
			loc.Query().Get("openid.realm") != "http://localhost:8080" || state == "" {
			t.Fatalf("unexpected Steam redirect: %s", loc)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == steamStateCookie {
				return state, c
			}
		}
		t.Fatal("no state cookie")
		return "", nil
	}

	// callback simulates Steam redirecting back, with overrides applied.
	callback := func(state string, cookies []*http.Cookie, override map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		q := url.Values{
			"state":                 {state},
			"openid.ns":             {openIDNS},
			"openid.mode":           {"id_res"},
			"openid.op_endpoint":    {steamOpenIDURL},
			"openid.claimed_id":     {"https://steamcommunity.com/openid/id/76561197960287930"},
			"openid.identity":       {"https://steamcommunity.com/openid/id/76561197960287930"},
			"openid.return_to":      {s.steamReturnTo(state)},
			"openid.response_nonce": {"2026-09-29T00:00:00Zabc"},
			"openid.assoc_handle":   {"1234567890"},
			"openid.signed":         {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"},
			"openid.sig":            {"good"},
		}
		for k, v := range override {
			if v == "" {
				q.Del(k)
			} else {
				q.Set(k, v)
			}
		}
		return do(t, h, http.MethodGet, "/auth/steam/callback?"+q.Encode(), nil, cookies...)
	}

	expectRedirect := func(rec *httptest.ResponseRecorder, want string) {
		t.Helper()
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusFound || !strings.Contains(loc, want) {
			t.Errorf("got %d %q, want redirect containing %q", rec.Code, loc, want)
		}
	}

	// Rejected responses: nothing gets linked.
	rejects := map[string]map[string]string{
		"steam won't confirm":     {"openid.sig": "forged"},
		"claimed id elsewhere":    {"openid.claimed_id": "https://evil.example/openid/id/76561197960287930", "openid.identity": "https://evil.example/openid/id/76561197960287930"},
		"claimed id not steamid":  {"openid.claimed_id": "https://steamcommunity.com/openid/id/123", "openid.identity": "https://steamcommunity.com/openid/id/123"},
		"identity differs":        {"openid.identity": "https://steamcommunity.com/openid/id/76561197960287931"},
		"wrong op endpoint":       {"openid.op_endpoint": "https://evil.example/openid/login"},
		"wrong return_to":         {"openid.return_to": "https://evil.example/cb"},
		"not a positive response": {"openid.mode": "setup_needed"},
	}
	for name, override := range rejects {
		state, sc := start()
		expectRedirect(callback(state, []*http.Cookie{sc, discordCookie}, override), "steam_error=verify_failed")
		if len(store.links) != 0 {
			t.Fatalf("%s: linked anyway", name)
		}
	}

	// State checks.
	state, sc := start()
	expectRedirect(callback(state, []*http.Cookie{discordCookie}, nil), "steam_error=invalid_state") // no state cookie
	state, sc = start()
	otherSession, _ := s.sessions.create(ctx, session{UserID: 99})
	expectRedirect(callback(state, []*http.Cookie{sc, {Name: sessionCookie, Value: otherSession}}, nil), "steam_error=not_logged_in")

	// Cancelled on Steam's page.
	state, sc = start()
	expectRedirect(callback(state, []*http.Cookie{sc, discordCookie}, map[string]string{"openid.mode": "cancel"}), "steam_error=cancelled")

	// Success: linked, and back to where the user started.
	state, sc = start()
	rec := callback(state, []*http.Cookie{sc, discordCookie}, nil)
	expectRedirect(rec, webOrigin+"/guilds/7?steam=linked")
	if store.links[42] != 76561197960287930 {
		t.Fatalf("links = %v", store.links)
	}

	// The same state can't be used twice.
	expectRedirect(callback(state, []*http.Cookie{sc, discordCookie}, nil), "steam_error=invalid_state")

	// /api/me/steam shows it, with the SteamID as a string.
	rec = do(t, h, http.MethodGet, "/api/me/steam", nil, discordCookie)
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["linked"] != true || view["steam_id"] != "76561197960287930" || view["profile_url"] != "https://steamcommunity.com/profiles/76561197960287930" {
		t.Errorf("/api/me/steam = %s", rec.Body.String())
	}

	// Unlink (a write: must come from the web app).
	if rec := do(t, h, http.MethodDelete, "/api/me/steam", nil, discordCookie); rec.Code != http.StatusForbidden {
		t.Errorf("unlink without Origin = %d, want 403", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/me/steam", map[string]string{"Origin": webOrigin}, discordCookie); rec.Code != http.StatusNoContent {
		t.Errorf("unlink = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/me/steam", nil, discordCookie)
	if !strings.Contains(rec.Body.String(), `"linked":false`) {
		t.Errorf("after unlink /api/me/steam = %s", rec.Body.String())
	}

	// Not logged in with Discord: sent back to the app, not to Steam.
	rec = do(t, h, http.MethodGet, "/auth/steam/link?next=/guilds/7", nil)
	expectRedirect(rec, webOrigin+"/guilds/7?steam_error=not_logged_in")
}
