package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// TestWowSyncSettings covers the admin endpoints. Needs Redis/Valkey and
// MariaDB: skipped unless BXT_TEST_REDIS_ADDR and BXT_TEST_DB_PORT are set.
func TestWowSyncSettings(t *testing.T) {
	addr := os.Getenv("BXT_TEST_REDIS_ADDR")
	port, _ := strconv.Atoi(os.Getenv("BXT_TEST_DB_PORT"))
	if addr == "" || port == 0 {
		t.Skip("BXT_TEST_REDIS_ADDR / BXT_TEST_DB_PORT not set")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer rdb.Close()
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(config.DBConfig{Host: "127.0.0.1", Port: port, User: "root", Password: "t", Name: "t", PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"wow_sync_settings", "wow_sync_guilds", "wow_sync_rank_roles", "wow_sync_rank_names"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatal(err)
		}
	}

	discordAPI := fakeDiscord(t)
	defer discordAPI.Close()
	blizz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ns := r.URL.Query().Get("namespace")
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"app","expires_in":86399}`))
		case r.URL.Path == "/us/data/wow/guild/area-52/loot-council/roster" && ns == "profile-us":
			_, _ = w.Write([]byte(`{"guild":{"name":"Loot Council","realm":{"name":"Area 52","slug":"area-52"}},"members":[
				{"character":{"name":"Gm","id":1},"rank":0},
				{"character":{"name":"Off1","id":2},"rank":1},{"character":{"name":"Off2","id":3},"rank":1},
				{"character":{"name":"R1","id":4},"rank":4},{"character":{"name":"R2","id":5},"rank":4},
				{"character":{"name":"R3","id":6},"rank":4},{"character":{"name":"R4","id":7},"rank":4}]}`))
		case r.URL.Path == "/us/data/wow/guild/area-52/loot-council-two/roster" && ns == "profile-us":
			_, _ = w.Write([]byte(`{"guild":{"name":"Loot Council Two","realm":{"name":"Area 52","slug":"area-52"}},"members":[
				{"character":{"name":"Alt","id":8},"rank":0},{"character":{"name":"Alt2","id":9},"rank":2}]}`))
		case r.URL.Path == "/us/data/wow/guild/faerlina/old-school/roster" && ns == "profile-classic-us":
			_, _ = w.Write([]byte(`{"guild":{"name":"Old School","realm":{"name":"Faerlina","slug":"faerlina"}},"members":[
				{"character":{"name":"Cgm","id":1},"rank":0},{"character":{"name":"Coff","id":2},"rank":1}]}`))
		case r.URL.Path == "/us/data/wow/guild/whitemane/era-guild/roster" && ns == "profile-classic1x-us":
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path == "/us/data/wow/realm/index" && ns == "dynamic-us":
			_, _ = w.Write([]byte(`{"realms":[{"name":"Zul'jin","slug":"zuljin"},{"name":"Area 52","slug":"area-52"}]}`))
		case r.URL.Path == "/us/data/wow/realm/index" && ns == "dynamic-classic-us":
			_, _ = w.Write([]byte(`{"realms":[{"name":"Faerlina","slug":"faerlina"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer blizz.Close()

	cfg := config.Config{
		Discord:   config.DiscordConfig{Token: "bot-token", ClientID: "1508701143583166575", ClientSecret: "secret"},
		API:       config.APIConfig{PublicURL: "http://localhost:8080", WebURL: webOrigin},
		BattleNet: config.BattleNetConfig{ClientID: "bnet-id", ClientSecret: "bnet-secret", Regions: "us", Flavours: "retail,classic,classic_era"},
	}
	s, err := New(cfg, db, rdb, withDiscordURL(discordAPI.URL), withBattlenet(blizz.URL, blizz.URL+"/%s", newMemBattlenetStore()))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	sid, _ := s.sessions.create(ctx, session{UserID: 42, OAuth: oauth2.Session{AccessToken: "ok", Scopes: scopes, Expiration: time.Now().Add(time.Hour)}})
	user := &http.Cookie{Name: sessionCookie, Value: sid}

	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", webOrigin)
		req.AddCookie(user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder) wowSyncView {
		var v wowSyncView
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return v
	}

	// Admins only (guild 2: the user isn't an admin there).
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/guilds/2/wow-sync"}, {"PUT", "/api/guilds/2/wow-sync"}, {"POST", "/api/guilds/2/wow-sync/guilds"},
		{"DELETE", "/api/guilds/2/wow-sync/guilds/1"}, {"GET", "/api/guilds/2/roles"},
		{"PUT", "/api/guilds/2/wow-sync/guilds/1/ranks"}, {"POST", "/api/guilds/2/wow-sync/run"},
	} {
		if rec := send(c.method, c.path, `{}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin = %d, want 403", c.method, c.path, rec.Code)
		}
	}

	// Realms per flavour.
	if rec := send("GET", "/api/guilds/1/wow-sync/realms?flavour=retail&region=us", ""); strings.Index(rec.Body.String(), "Area 52") > strings.Index(rec.Body.String(), "Zul'jin") || !strings.Contains(rec.Body.String(), "Area 52") {
		t.Errorf("retail realms = %s", rec.Body.String())
	}
	if rec := send("GET", "/api/guilds/1/wow-sync/realms?flavour=classic&region=us", ""); !strings.Contains(rec.Body.String(), "Faerlina") {
		t.Errorf("classic realms = %s", rec.Body.String())
	}
	if rec := send("GET", "/api/guilds/1/wow-sync/realms?flavour=forever&region=us", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad flavour realms = %d", rec.Code)
	}

	// Defaults before anything is saved.
	v := decode(send("GET", "/api/guilds/1/wow-sync", ""))
	if v.Settings.Enabled || v.Settings.NicknameMode != "off" || !v.Settings.RemoveRoles || len(v.Flavours) != 3 || len(v.Guilds) != 0 {
		t.Errorf("defaults = %+v", v)
	}

	// Link guilds: bad flavour and unknown guilds refused; two retail guilds
	// and a classic one linked with Blizzard's names; a duplicate refused.
	add := func(body string) *httptest.ResponseRecorder {
		return send("POST", "/api/guilds/1/wow-sync/guilds", body)
	}
	if rec := add(`{"flavour":"forever","region":"us","realm_slug":"area-52","guild":"Loot Council"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad flavour = %d", rec.Code)
	}
	if rec := add(`{"flavour":"retail","region":"us","realm_slug":"area-52","guild":"Nope"}`); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "wow_guild_not_found") {
		t.Errorf("unknown guild = %d %s", rec.Code, rec.Body.String())
	}
	if rec := add(`{"flavour":"classic_era","region":"us","realm_slug":"whitemane","guild":"Era Guild"}`); !strings.Contains(rec.Body.String(), "blizzard_forbidden") {
		t.Errorf("forbidden era guild = %d %s", rec.Code, rec.Body.String())
	}
	add(`{"flavour":"retail","region":"us","realm_slug":"area-52","guild":"loot council"}`)
	add(`{"flavour":"classic","region":"us","realm_slug":"faerlina","guild":"Old School"}`)
	v = decode(add(`{"flavour":"retail","region":"us","realm_slug":"area-52","guild":"Loot Council Two"}`))
	if len(v.Guilds) != 3 {
		t.Fatalf("linked guilds = %+v", v.Guilds)
	}
	// Ordered by flavour, then name.
	retail, retail2, classic := v.Guilds[0], v.Guilds[1], v.Guilds[2]
	if retail.WowGuildName != "Loot Council" || retail2.WowGuildName != "Loot Council Two" || classic.WowGuildName != "Old School" ||
		classic.RealmName != "Faerlina" || classic.Flavour != "classic" || retail.ID == 0 || retail.ID == retail2.ID {
		t.Fatalf("linked guilds = %+v", v.Guilds)
	}
	if rec := add(`{"flavour":"retail","region":"us","realm_slug":"area-52","guild":"Loot Council"}`); rec.Code != http.StatusConflict {
		t.Errorf("duplicate guild = %d, want 409", rec.Code)
	}

	// Settings.
	if rec := send("PUT", "/api/guilds/1/wow-sync", `{"enabled":true,"nickname_mode":"forever","remove_roles":true}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad nickname mode = %d", rec.Code)
	}
	v = decode(send("PUT", "/api/guilds/1/wow-sync", `{"enabled":true,"nickname_mode":"combined","remove_roles":false}`))
	if !v.Settings.Enabled || v.Settings.NicknameMode != "combined" || v.Settings.RemoveRoles {
		t.Errorf("settings = %+v", v.Settings)
	}

	// Ranks from every linked guild, keyed by link (the first retail guild's
	// ranks were cached when it was linked; the rest are read now).
	key := func(l int64, rank int) string { return strconv.FormatInt(l, 10) + ":" + strconv.Itoa(rank) }
	rec := send("GET", "/api/guilds/1/wow-sync/ranks", "")
	var ranks ranksResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &ranks)
	var keys []string
	for _, r := range ranks.Ranks {
		keys = append(keys, r.Key+"="+strconv.Itoa(r.Members))
	}
	want := strings.Join([]string{key(retail.ID, 0) + "=1", key(retail.ID, 1) + "=2", key(retail.ID, 4) + "=4",
		key(retail2.ID, 0) + "=1", key(retail2.ID, 2) + "=1", key(classic.ID, 0) + "=1", key(classic.ID, 1) + "=1"}, ",")
	if strings.Join(keys, ",") != want || len(ranks.Errors) != 0 {
		t.Errorf("ranks = %s, want %s", rec.Body.String(), want)
	}

	// Rank names and roles, saved per linked guild. The same Discord role
	// from several guilds is fine; a role above the bot is refused.
	ranksPath := func(l int64) string { return "/api/guilds/1/wow-sync/guilds/" + strconv.FormatInt(l, 10) + "/ranks" }
	if rec := send("PUT", ranksPath(retail.ID), `{"ranks":{"0":{"role_id":"70"}}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("role above bot = %d, want 422", rec.Code)
	}
	if rec := send("PUT", ranksPath(retail.ID), `{"ranks":{"x":{"name":"Oops"}}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad rank = %d, want 400", rec.Code)
	}
	if rec := send("PUT", ranksPath(retail.ID), `{"ranks":{"1":{"name":"`+strings.Repeat("n", 65)+`"}}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("rank name too long = %d, want 400", rec.Code)
	}
	if rec := send("PUT", ranksPath(999999), `{"ranks":{}}`); rec.Code != http.StatusNotFound {
		t.Errorf("ranks for unknown link = %d, want 404", rec.Code)
	}
	send("PUT", ranksPath(retail.ID), `{"ranks":{
		"0":{"name":"Guild Master"},"1":{"name":" Officer ","role_id":"80"},"4":{"name":"Raider","role_id":"90"}}}`)
	send("PUT", ranksPath(retail2.ID), `{"ranks":{"2":{"name":"Alt Raider","role_id":"90"}}}`)
	v = decode(send("PUT", ranksPath(classic.ID), `{"ranks":{"1":{"name":"Classic Officer","role_id":"80"}}}`))
	if v.RankRoles[key(retail.ID, 1)] != "80" || v.RankRoles[key(classic.ID, 1)] != "80" || v.RankRoles[key(retail.ID, 4)] != "90" ||
		v.RankRoles[key(retail2.ID, 2)] != "90" || len(v.RankRoles) != 4 {
		t.Errorf("mapping = %v", v.RankRoles)
	}
	if v.RankNames[key(retail.ID, 0)] != "Guild Master" || v.RankNames[key(retail.ID, 1)] != "Officer" || v.RankNames[key(classic.ID, 1)] != "Classic Officer" {
		t.Errorf("rank names = %v", v.RankNames)
	}
	// Saving one guild's ranks leaves the others alone.
	v = decode(send("PUT", ranksPath(classic.ID), `{"ranks":{}}`))
	if v.RankRoles[key(classic.ID, 1)] != "" || v.RankNames[key(classic.ID, 1)] != "" || v.RankRoles[key(retail.ID, 1)] != "80" || v.RankNames[key(retail2.ID, 2)] != "Alt Raider" {
		t.Errorf("after clearing classic ranks: roles %v names %v", v.RankRoles, v.RankNames)
	}

	if rec := send("POST", "/api/guilds/1/wow-sync/run", ""); rec.Code != http.StatusAccepted {
		t.Errorf("run = %d", rec.Code)
	}

	// Unlinking the second retail guild drops its mappings and names only.
	v = decode(send("DELETE", "/api/guilds/1/wow-sync/guilds/"+strconv.FormatInt(retail2.ID, 10), ""))
	if len(v.Guilds) != 2 || v.RankRoles[key(retail2.ID, 2)] != "" || v.RankNames[key(retail2.ID, 2)] != "" || v.RankRoles[key(retail.ID, 1)] != "80" {
		t.Errorf("after unlinking: guilds %v, mapping %v", v.Guilds, v.RankRoles)
	}
	if rec := send("DELETE", "/api/guilds/1/wow-sync/guilds/"+strconv.FormatInt(retail2.ID, 10), ""); rec.Code != http.StatusNotFound {
		t.Errorf("unlink twice = %d, want 404", rec.Code)
	}
}
