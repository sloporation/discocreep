package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

type primaryKey struct {
	guild, user snowflake.ID
	flavour     blizzard.Flavour
}

// memBattlenetStore is an in-memory battlenetStore with the same ownership rules.
type memBattlenetStore struct {
	mu        sync.Mutex
	links     map[snowflake.ID]battlenetLink
	chars     map[snowflake.ID][]wowCharacter
	primaries map[primaryKey]wowCharacter
	saves     int
	synced    []blizzard.Flavour // flavours replaced by the last save
}

func newMemBattlenetStore() *memBattlenetStore {
	return &memBattlenetStore{links: map[snowflake.ID]battlenetLink{}, chars: map[snowflake.ID][]wowCharacter{}, primaries: map[primaryKey]wowCharacter{}}
}

func (m *memBattlenetStore) get(_ context.Context, u snowflake.ID) (*battlenetLink, []wowCharacter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[u]
	if !ok {
		return nil, nil, nil
	}
	return &l, m.chars[u], nil
}

func (m *memBattlenetStore) save(_ context.Context, u snowflake.ID, id uint64, tag string, chars []wowCharacter, synced []blizzard.Flavour) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.links[u] = battlenetLink{BattlenetID: id, BattleTag: tag, LinkedAt: now, SyncedAt: &now}
	var kept []wowCharacter
	for _, c := range m.chars[u] {
		if !slices.Contains(synced, c.Flavour) {
			kept = append(kept, c)
		}
	}
	m.chars[u] = append(kept, chars...)
	m.saves++
	m.synced = synced
	return nil
}

func (m *memBattlenetStore) unlink(_ context.Context, u snowflake.ID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.links[u]
	delete(m.links, u)
	delete(m.chars, u)
	for k := range m.primaries {
		if k.user == u {
			delete(m.primaries, k)
		}
	}
	return ok, nil
}

func (m *memBattlenetStore) getPrimaries(_ context.Context, g, u snowflake.ID) (map[blizzard.Flavour]wowCharacter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[blizzard.Flavour]wowCharacter{}
	for k, c := range m.primaries {
		if k.guild == g && k.user == u {
			out[k.flavour] = c
		}
	}
	return out, nil
}

func (m *memBattlenetStore) setPrimary(_ context.Context, g, u snowflake.ID, f blizzard.Flavour, region string, id uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.chars[u] {
		if c.Flavour == f && c.Region == region && c.ID == id {
			m.primaries[primaryKey{g, u, f}] = c
			return nil
		}
	}
	return errCharacterNotOwned
}

func (m *memBattlenetStore) clearPrimary(_ context.Context, g, u snowflake.ID, f blizzard.Flavour) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.primaries, primaryKey{g, u, f})
	return nil
}

// fakeBlizzard serves OAuth and the Profile API for every flavour.
// nsStatus overrides a namespace's response status (e.g. 403 for Classic Era).
func fakeBlizzard(t *testing.T, nsStatus map[string]int) *httptest.Server {
	char := func(name string, id, level int, realm, realmSlug, class string, classID int, race, faction string) string {
		return fmt.Sprintf(`{"name":%q,"id":%d,"level":%d,"realm":{"name":%q,"slug":%q},"playable_class":{"name":%q,"id":%d},"playable_race":{"name":%q},"faction":{"type":%q}}`,
			name, id, level, realm, realmSlug, class, classID, race, faction)
	}
	profiles := map[string]string{
		"us/profile-us": char("Lowbie", 101, 12, "Area 52", "area-52", "Rogue", 4, "Orc", "HORDE") + "," +
			char("Mainchar", 102, 80, "Area 52", "area-52", "Paladin", 2, "Blood Elf", "HORDE"),
		"kr/profile-kr":           char("Seoulmage", 101, 70, "Azshara", "azshara", "Mage", 8, "Human", "ALLIANCE"),
		"us/profile-classic-us":   char("Classicguy", 101, 85, "Faerlina", "faerlina", "Warrior", 1, "Orc", "HORDE"),
		"us/profile-classic1x-us": char("Eraguy", 900, 60, "Whitemane", "whitemane", "Priest", 5, "Undead", "HORDE"),
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			user, pass, ok := r.BasicAuth()
			_ = r.ParseForm()
			if !ok || user != "bnet-id" || pass != "bnet-secret" {
				t.Errorf("bad token request auth")
			}
			switch {
			case r.Form.Get("grant_type") == "client_credentials":
				_, _ = w.Write([]byte(`{"access_token":"app","expires_in":86399}`))
			case r.Form.Get("code") == "good-code" && r.Form.Get("redirect_uri") == "http://localhost:8080/auth/battlenet/callback":
				_, _ = w.Write([]byte(`{"access_token":"bnet-token","token_type":"bearer","expires_in":86399}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			}
		case r.URL.Path == "/oauth/userinfo" && r.Header.Get("Authorization") == "Bearer bnet-token":
			_, _ = w.Write([]byte(`{"sub":"555","id":555,"battletag":"Alice#1234"}`))
		case strings.HasSuffix(r.URL.Path, "/profile/user/wow") && r.Header.Get("Authorization") == "Bearer bnet-token":
			region := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[0]
			ns := r.URL.Query().Get("namespace")
			if st, ok := nsStatus[ns]; ok {
				w.WriteHeader(st)
				return
			}
			body, ok := profiles[region+"/"+ns]
			if !ok { // no account for this flavour in this region
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"wow_accounts":[{"characters":[` + body + `]}]}`))
		default:
			t.Errorf("unexpected Blizzard call: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestBattlenetFlow runs Battle.net linking and mains end to end against
// fake Blizzard and Discord. Needs a real Redis/Valkey: skipped unless
// BXT_TEST_REDIS_ADDR is set.
func TestBattlenetFlow(t *testing.T) {
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
	nsStatus := map[string]int{}
	blizz := fakeBlizzard(t, nsStatus)
	defer blizz.Close()

	store := newMemBattlenetStore()
	cfg := config.Config{
		Discord:   config.DiscordConfig{Token: "bot-token", ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:       config.APIConfig{PublicURL: "http://localhost:8080", WebURL: webOrigin},
		BattleNet: config.BattleNetConfig{ClientID: "bnet-id", ClientSecret: "bnet-secret", Regions: "us,eu,kr,tw", Flavours: "retail,classic,classic_era"},
	}
	s, err := New(cfg, nil, rdb, withDiscordURL(discordAPI.URL), withBattlenet(blizz.URL, blizz.URL+"/%s", store))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	sessionID, err := s.sessions.create(ctx, session{
		UserID: 42, Username: "alice",
		OAuth: oauth2.Session{AccessToken: "ok", Scopes: scopes, Expiration: time.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	user := &http.Cookie{Name: sessionCookie, Value: sessionID}

	start := func() (string, *http.Cookie) {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/auth/battlenet/link?next=/account", nil, user)
		loc, _ := url.Parse(rec.Header().Get("Location"))
		q := loc.Query()
		if rec.Code != http.StatusFound || loc.Path != "/authorize" || q.Get("client_id") != "bnet-id" ||
			q.Get("scope") != "openid wow.profile" || q.Get("state") == "" {
			t.Fatalf("unexpected authorize redirect: %d %s", rec.Code, loc)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == battlenetStateCookie {
				return q.Get("state"), c
			}
		}
		t.Fatal("no state cookie")
		return "", nil
	}
	callback := func(query string, cookies ...*http.Cookie) string {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/auth/battlenet/callback?"+query, nil, cookies...)
		if rec.Code != http.StatusFound {
			t.Fatalf("callback = %d", rec.Code)
		}
		return rec.Header().Get("Location")
	}
	expect := func(loc, want string) {
		t.Helper()
		if !strings.Contains(loc, want) {
			t.Errorf("redirect %q, want it to contain %q", loc, want)
		}
	}

	// Failures: nothing saved.
	state, sc := start()
	expect(callback("state="+state+"&error=access_denied", sc, user), "battlenet_error=cancelled")
	state, sc = start()
	expect(callback("state="+state+"&code=bad-code", sc, user), "battlenet_error=exchange_failed")
	state, _ = start()
	expect(callback("state="+state+"&code=good-code", user), "battlenet_error=invalid_state")
	for _, ns := range []string{"profile-us", "profile-kr", "profile-classic-us", "profile-classic1x-us"} {
		nsStatus[ns] = http.StatusInternalServerError
	}
	state, sc = start()
	expect(callback("state="+state+"&code=good-code", sc, user), "battlenet_error=blizzard_error") // every flavour failed
	clear(nsStatus)
	if store.saves != 0 {
		t.Fatalf("saved %d times after failures", store.saves)
	}

	// Classic Era fails (like Blizzard's 403s): retail + Classic still link.
	nsStatus["profile-classic1x-us"] = http.StatusForbidden
	state, sc = start()
	loc := callback("state="+state+"&code=good-code", sc, user)
	expect(loc, "battlenet=linked")
	expect(loc, "battlenet_skipped=classic_era")
	if !slices.Equal(store.synced, []blizzard.Flavour{blizzard.Retail, blizzard.Classic}) {
		t.Errorf("synced flavours = %v", store.synced)
	}
	// The same state is single-use.
	expect(callback("state="+state+"&code=good-code", sc, user), "battlenet_error=invalid_state")

	rec := do(t, h, http.MethodGet, "/api/me/battlenet", nil, user)
	var view battlenetView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range view.Characters {
		names = append(names, fmt.Sprintf("%s:%s:%s", c.Flavour, c.Region, c.Name))
	}
	// Sorted by level; the same character ID in different flavours/regions is a different character.
	if !view.Linked || view.BattleTag != "Alice#1234" || len(view.Flavours) != 3 ||
		strings.Join(names, ",") != "classic:us:Classicguy,retail:us:Mainchar,retail:kr:Seoulmage,retail:us:Lowbie" {
		t.Fatalf("/api/me/battlenet = %s (%v)", rec.Body.String(), names)
	}

	// Refresh with Era working: its characters are added, the rest replaced.
	clear(nsStatus)
	state, sc = start()
	expect(callback("state="+state+"&code=good-code", sc, user), "battlenet=linked")
	if _, chars, _ := store.get(ctx, 42); len(chars) != 5 {
		t.Errorf("after refresh %d characters, want 5", len(chars))
	}

	// Mains: one per flavour, per guild; any member of a shared guild.
	origin := map[string]string{"Origin": webOrigin}
	put := func(guild, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/guilds/"+guild+"/wow-character", strings.NewReader(body))
		req.Header.Set("Origin", webOrigin)
		req.AddCookie(user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := put("2", `{"flavour":"retail","region":"us","character_id":102}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Mainchar") {
		t.Errorf("set retail main = %d %s", rec.Code, rec.Body.String())
	}
	rec = put("2", `{"flavour":"classic","region":"us","character_id":101}`)
	var prim struct {
		Characters map[blizzard.Flavour]*wowCharacter `json:"characters"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &prim)
	if rec.Code != http.StatusOK || prim.Characters["retail"] == nil || prim.Characters["classic"] == nil ||
		prim.Characters["classic"].Name != "Classicguy" || prim.Characters["classic_era"] != nil {
		t.Errorf("set classic main = %d %s", rec.Code, rec.Body.String())
	}
	// Retail character ID 101 is Lowbie, not Classicguy: flavour matters.
	if rec := put("2", `{"flavour":"classic","region":"us","character_id":102}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("retail character as classic main = %d, want 422", rec.Code)
	}
	if rec := put("2", `{"flavour":"forever","region":"us","character_id":101}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad flavour = %d, want 400", rec.Code)
	}
	if rec := put("9", `{"flavour":"retail","region":"us","character_id":102}`); rec.Code != http.StatusNotFound {
		t.Errorf("guild not shared = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodDelete, "/api/guilds/2/wow-character?flavour=classic", origin, user); rec.Code != http.StatusNoContent {
		t.Errorf("clear classic main = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/guilds/2/wow-character", nil, user)
	prim.Characters = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &prim)
	if prim.Characters["retail"] == nil || prim.Characters["classic"] != nil {
		t.Errorf("after clearing classic = %s", rec.Body.String())
	}

	// Unlink removes the link, characters and mains.
	if rec := do(t, h, http.MethodDelete, "/api/me/battlenet", origin, user); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/me/battlenet", nil, user); !strings.Contains(rec.Body.String(), `"linked":false`) {
		t.Errorf("after unlink = %s", rec.Body.String())
	}
	if len(store.primaries) != 0 {
		t.Errorf("mains left after unlink: %v", store.primaries)
	}

	// Not configured: the feature reports disabled and its routes 404.
	cfg.BattleNet = config.BattleNetConfig{}
	off, err := New(cfg, nil, rdb, withDiscordURL(discordAPI.URL))
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(t, off.Handler(), http.MethodGet, "/api/me/battlenet", nil, user); !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("disabled /api/me/battlenet = %s", rec.Body.String())
	}
	if rec := do(t, off.Handler(), http.MethodGet, "/auth/battlenet/link", nil, user); rec.Code != http.StatusNotFound {
		t.Errorf("disabled link = %d, want 404", rec.Code)
	}
}
