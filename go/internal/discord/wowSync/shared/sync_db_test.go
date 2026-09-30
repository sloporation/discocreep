package shared

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// TestSyncEndToEnd runs Syncer.Sync against a real MariaDB, fake Blizzard
// rosters (retail, Classic, and a Classic Era roster that 403s) and a fake
// Discord that records member updates. Skipped unless BXT_TEST_DB_PORT is
// set (throwaway MariaDB, root password "t").
func TestSyncEndToEnd(t *testing.T) {
	port, _ := strconv.Atoi(os.Getenv("BXT_TEST_DB_PORT"))
	if port == 0 {
		t.Skip("BXT_TEST_DB_PORT not set")
	}
	// Own database: the api package's DB tests use "t" and packages run in parallel.
	root, err := database.Open(config.DBConfig{Host: "127.0.0.1", Port: port, User: "root", Password: "t", Name: "t", PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.ExecContext(context.Background(), "CREATE DATABASE IF NOT EXISTS t_wowsync"); err != nil {
		t.Fatal(err)
	}
	root.Close()
	db, err := database.Open(config.DBConfig{Host: "127.0.0.1", Port: port, User: "root", Password: "t", Name: "t_wowsync", PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tbl := range []string{"wow_sync_settings", "wow_sync_guilds", "wow_sync_rank_roles", "wow_sync_rank_names", "wow_characters", "wow_primary_characters", "wow_sync_role_log", "guilds"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatal(err)
		}
	}

	const (
		guildID                            = snowflake.ID(1000)
		officer, raider, eraRole, unrelate = "501", "502", "503", "900"
		alice, bob, carol, dave, erin      = snowflake.ID(11), snowflake.ID(12), snowflake.ID(13), snowflake.ID(14), snowflake.ID(15)
		frank                              = snowflake.ID(16) // in the second retail guild
		altRole                            = "504"
	)

	// Blizzard rosters. Retail: Alice rank 4. Classic: Alice and Bob rank 1.
	// Classic Era: 403, like Blizzard's broken Era guild API.
	blizz := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		ns := r.URL.Query().Get("namespace")
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"access_token":"app","expires_in":86399}`))
		case r.URL.Path == "/us/data/wow/guild/area-52/retail-two/roster" && ns == "profile-us":
			_, _ = w.Write([]byte(`{"guild":{"name":"Retail Two"},"members":[{"character":{"name":"Frankr","id":250},"rank":2}]}`))
		case r.URL.Path == "/us/data/wow/guild/area-52/retail-guild/roster" && ns == "profile-us":
			_, _ = w.Write([]byte(`{"guild":{"name":"Retail Guild"},"members":[{"character":{"name":"Alicer","id":201},"rank":4}]}`))
		case r.URL.Path == "/us/data/wow/guild/faerlina/classic-guild/roster" && ns == "profile-classic-us":
			_, _ = w.Write([]byte(`{"guild":{"name":"Classic Guild"},"members":[
				{"character":{"name":"Alicec","id":301},"rank":1},{"character":{"name":"Bobc","id":302},"rank":1}]}`))
		case r.URL.Path == "/us/data/wow/guild/whitemane/era-guild/roster" && ns == "profile-classic1x-us":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":403,"type":"BLZWEBAPI00000403"}`))
		default:
			t.Errorf("unexpected Blizzard call %s (namespace %s)", r.URL.Path, ns)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer blizz.Close()

	var mu sync.Mutex
	members := map[snowflake.ID]map[string]any{
		alice: {"roles": []any{unrelate}, "nick": nil},          // gains officer (both guilds) + raider (retail)
		bob:   {"roles": []any{officer, raider}, "nick": nil},   // keeps officer via Classic; loses raider
		carol: {"roles": []any{officer, unrelate}, "nick": nil}, // in neither guild: loses officer
		dave:  {"roles": []any{eraRole}, "nick": nil},           // Era roster unreadable: keeps eraRole
		erin:  {"roles": []any{officer}, "nick": nil},           // no mains: untouched
		frank: {"roles": []any{}, "nick": nil},                  // second retail guild, rank 2 → altRole
	}
	patches := map[snowflake.ID]map[string]any{}
	discordAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "guilds":
			_, _ = w.Write([]byte(`{"id":"1000","name":"Server","owner_id":"1"}`))
		case len(parts) == 4 && parts[2] == "members":
			uid, _ := snowflake.Parse(parts[3])
			m, ok := members[uid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":10007,"message":"Unknown Member"}`))
				return
			}
			if r.Method == http.MethodPatch {
				var body map[string]any
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &body)
				patches[uid] = body
				for k, v := range body {
					m[k] = v
				}
			}
			out, _ := json.Marshal(map[string]any{"user": map[string]any{"id": uid.String(), "username": "u"}, "roles": m["roles"], "nick": m["nick"]})
			_, _ = w.Write(out)
		default:
			t.Errorf("unexpected Discord call %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer discordAPI.Close()

	client, err := disgo.New("MTAwMA.fake.token", bot.WithRestClientConfigOpts(rest.WithURL(discordAPI.URL)))
	if err != nil {
		t.Fatal(err)
	}

	// Settings, three linked guilds, the rank → role table and mains.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(SaveSettings(ctx, db, guildID, Settings{Enabled: true, NicknameMode: NicknameCombined, RemoveRoles: true}))
	add := func(l GuildLink) int64 {
		t.Helper()
		id, err := AddGuild(ctx, db, guildID, l)
		must(err)
		return id
	}
	retailID := add(GuildLink{Flavour: blizzard.Retail, Region: "us", RealmSlug: "area-52", RealmName: "Area 52", WowGuildSlug: "retail-guild", WowGuildName: "Retail Guild"})
	retail2ID := add(GuildLink{Flavour: blizzard.Retail, Region: "us", RealmSlug: "area-52", RealmName: "Area 52", WowGuildSlug: "retail-two", WowGuildName: "Retail Two"})
	classicID := add(GuildLink{Flavour: blizzard.Classic, Region: "us", RealmSlug: "faerlina", RealmName: "Faerlina", WowGuildSlug: "classic-guild", WowGuildName: "Classic Guild"})
	eraID := add(GuildLink{Flavour: blizzard.ClassicEra, Region: "us", RealmSlug: "whitemane", RealmName: "Whitemane", WowGuildSlug: "era-guild", WowGuildName: "Era Guild"})
	if _, err := AddGuild(ctx, db, guildID, GuildLink{Flavour: blizzard.Retail, Region: "us", RealmSlug: "area-52", RealmName: "Area 52", WowGuildSlug: "retail-guild", WowGuildName: "Retail Guild"}); err != ErrDuplicateGuild {
		t.Errorf("duplicate AddGuild = %v, want ErrDuplicateGuild", err)
	}
	id := func(s string) snowflake.ID { v, _ := snowflake.Parse(s); return v }
	must(SaveGuildRanks(ctx, db, guildID, retailID, map[int]RankSetting{1: {Name: "Officer", RoleID: id(officer)}, 4: {Name: "Raider", RoleID: id(raider)}}))
	must(SaveGuildRanks(ctx, db, guildID, retail2ID, map[int]RankSetting{2: {Name: "Alt", RoleID: id(altRole)}}))
	must(SaveGuildRanks(ctx, db, guildID, classicID, map[int]RankSetting{1: {RoleID: id(officer)}})) // same Discord role from another guild
	must(SaveGuildRanks(ctx, db, guildID, eraID, map[int]RankSetting{0: {RoleID: id(eraRole)}}))
	if err := SaveGuildRanks(ctx, db, 999, retailID, nil); err != ErrNoSuchLink {
		t.Errorf("SaveGuildRanks for another server's link = %v, want ErrNoSuchLink", err)
	}
	addMain := func(user snowflake.ID, f blizzard.Flavour, charID int, name string) {
		t.Helper()
		must(func() error {
			_, err := db.ExecContext(ctx, `INSERT INTO wow_characters (flavour, region, character_id, discord_user_id, name, realm_slug, realm_name, level, class_id, class_name, race_name, faction)
				VALUES (?, 'us', ?, ?, ?, 'x', 'X', 60, 1, 'Warrior', 'Orc', 'HORDE')`, f, charID, user, name)
			return err
		}())
		must(func() error {
			_, err := db.ExecContext(ctx, "INSERT INTO wow_primary_characters (guild_id, discord_user_id, flavour, region, character_id) VALUES (?, ?, ?, 'us', ?)", guildID, user, f, charID)
			return err
		}())
	}
	addMain(alice, blizzard.Retail, 201, "Alicer")
	addMain(alice, blizzard.Classic, 301, "Alicec")
	addMain(bob, blizzard.Retail, 299, "Bobr") // not in the retail guild
	addMain(bob, blizzard.Classic, 302, "Bobc")
	addMain(carol, blizzard.Retail, 298, "Carolr") // in no guild
	addMain(dave, blizzard.ClassicEra, 401, "Daveera")
	addMain(frank, blizzard.Retail, 250, "Frankr")

	bnet := &blizzard.Client{ClientID: "id", ClientSecret: "secret", OAuthURL: blizz.URL, APIURL: blizz.URL + "/%s"}
	syncer := NewSyncer(db, alerts.New(db), nil, bnet)
	syncer.Sync(ctx, client, guildID)

	roles := func(u snowflake.ID) []string {
		var out []string
		for _, r := range members[u]["roles"].([]any) {
			out = append(out, r.(string))
		}
		slices.Sort(out)
		return out
	}
	expect := func(u snowflake.ID, wantRoles []string, wantNick any) {
		t.Helper()
		slices.Sort(wantRoles)
		if got := roles(u); !slices.Equal(got, wantRoles) {
			t.Errorf("user %s roles = %v, want %v", u, got, wantRoles)
		}
		if members[u]["nick"] != wantNick {
			t.Errorf("user %s nick = %v, want %v", u, members[u]["nick"], wantNick)
		}
	}

	mu.Lock()
	expect(alice, []string{unrelate, officer, raider}, "Alicer / Alicec")
	expect(bob, []string{officer}, "Bobr / Bobc") // officer still granted by Classic
	expect(carol, []string{unrelate}, "Carolr")   // lost officer; unrelated role kept
	expect(dave, []string{eraRole}, "Daveera")    // Era unreadable: role kept
	expect(frank, []string{altRole}, "Frankr")    // rank role from the second retail guild
	if _, touched := patches[erin]; touched {
		t.Errorf("erin (no mains) was updated: %v", patches[erin])
	}
	mu.Unlock()

	_, _, st, _ := Load(ctx, db, guildID)
	if st.LastError != nil || st.LastResult == nil || st.LastResult.Linked != 5 || st.LastResult.InGuild != 3 ||
		!strings.Contains(st.LastResult.GuildErrors[eraID], "403") {
		t.Errorf("status = %v / %+v", st.LastError, st.LastResult)
	}

	// Every change is in the role log: adds, removes, and Bob's officer
	// role adopted (he already had it and his rank grants it).
	logOf := func(u snowflake.ID) []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, "SELECT action, role_id FROM wow_sync_role_log WHERE guild_id = ? AND discord_user_id = ? ORDER BY id", guildID, u)
		must(err)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var action, role string
			must(rows.Scan(&action, &role))
			out = append(out, action+":"+role)
		}
		return out
	}
	expectLog := func(u snowflake.ID, want ...string) {
		t.Helper()
		got := logOf(u)
		// Entries from one sync have no set order; compare sorted, per run.
		if len(got) != len(want) {
			t.Errorf("user %s log = %v, want %v", u, got, want)
			return
		}
		g, w := slices.Clone(got), slices.Clone(want)
		slices.Sort(g)
		slices.Sort(w)
		if !slices.Equal(g, w) {
			t.Errorf("user %s log = %v, want %v", u, got, want)
		}
	}
	expectLog(alice, "add:"+officer, "add:"+raider)
	expectLog(bob, "adopt:"+officer, "remove:"+raider)
	expectLog(carol, "remove:"+officer)
	expectLog(dave)
	expectLog(erin)
	expectLog(frank, "add:"+altRole)

	// A second run with nothing changed logs nothing.
	syncer.Sync(ctx, client, guildID)
	expectLog(alice, "add:"+officer, "add:"+raider)
	expectLog(bob, "adopt:"+officer, "remove:"+raider)

	// Taking a role off every rank takes it back from members the bot gave
	// it to. Clearing your mains takes back roles the bot gave you. A member
	// who left is released. Erin's hand-given officer role is still untouched.
	must(SaveGuildRanks(ctx, db, guildID, retail2ID, map[int]RankSetting{2: {Name: "Alt"}}))
	must(func() error {
		_, err := db.ExecContext(ctx, "DELETE FROM wow_primary_characters WHERE guild_id = ? AND discord_user_id = ?", guildID, alice)
		return err
	}())
	mu.Lock()
	delete(members, bob)
	clear(patches)
	mu.Unlock()
	syncer.Sync(ctx, client, guildID)
	mu.Lock()
	expect(frank, []string{}, "Frankr")
	expect(alice, []string{unrelate}, "Alicer / Alicec") // nickname left as it was
	if _, touched := patches[erin]; touched {
		t.Errorf("erin (no mains, nothing from the bot) was updated: %v", patches[erin])
	}
	mu.Unlock()
	if got := logOf(frank); got[len(got)-1] != "remove:"+altRole {
		t.Errorf("frank log = %v", got)
	}
	if got := logOf(alice); len(got) != 4 || !slices.Contains(got[2:], "remove:"+officer) || !slices.Contains(got[2:], "remove:"+raider) {
		t.Errorf("alice log = %v", got)
	}
	if got := logOf(bob); got[len(got)-1] != "release:"+officer {
		t.Errorf("bob log = %v", got)
	}
	held, err := HeldRoles(ctx, db, guildID)
	must(err)
	if len(held[alice]) != 0 || len(held[bob]) != 0 || len(held[frank]) != 0 {
		t.Errorf("held after taking back = %v", held)
	}

	// Frank gets the unmapped role back by hand: the bot didn't give it, so
	// it's left alone.
	mu.Lock()
	members[frank]["roles"] = []any{altRole}
	delete(patches, frank)
	mu.Unlock()
	syncer.Sync(ctx, client, guildID)
	mu.Lock()
	if _, touched := patches[frank]; touched {
		t.Errorf("frank's hand-given role was touched: %v", patches[frank])
	}
	mu.Unlock()

	// Auto-remove off: Carol gets officer back by hand and keeps it.
	must(SaveSettings(ctx, db, guildID, Settings{Enabled: true, NicknameMode: NicknameOff, RemoveRoles: false}))
	mu.Lock()
	members[carol]["roles"] = []any{officer, unrelate}
	delete(patches, carol)
	mu.Unlock()
	syncer.Sync(ctx, client, guildID)
	mu.Lock()
	if _, touched := patches[carol]; touched || !slices.Contains(roles(carol), officer) {
		t.Errorf("with auto-remove off, carol = %v (patched: %v)", members[carol], patches[carol])
	}
	mu.Unlock()
}
