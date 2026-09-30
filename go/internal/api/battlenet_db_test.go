package api

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// TestDBBattlenetStore exercises the Battle.net tables against a real
// MariaDB with migrations applied. Skipped unless BXT_TEST_DB_PORT is set
// (see TestDBSteamStore for how to start one).
func TestDBBattlenetStore(t *testing.T) {
	port, _ := strconv.Atoi(os.Getenv("BXT_TEST_DB_PORT"))
	if port == 0 {
		t.Skip("BXT_TEST_DB_PORT not set")
	}
	db, err := database.Open(config.DBConfig{Host: "127.0.0.1", Port: port, User: "root", Password: "t", Name: "t", PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tbl := range []string{"battlenet_links", "battlenet_link_history", "wow_characters", "wow_primary_characters"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatal(err)
		}
	}
	store := &dbBattlenetStore{db: db}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const r, c, e = blizzard.Retail, blizzard.Classic, blizzard.ClassicEra
	all := []blizzard.Flavour{r, c, e}
	char := func(f blizzard.Flavour, id uint64, name string, level int) wowCharacter {
		return wowCharacter{Flavour: f, Region: "us", ID: id, Name: name, RealmSlug: "area-52", RealmName: "Area 52", Level: level, ClassID: 2, ClassName: "Paladin", RaceName: "Blood Elf", Faction: "HORDE"}
	}

	// Link: the same character ID in two flavours is two characters.
	must(store.save(ctx, 42, 555, "Alice#1234", []wowCharacter{char(r, 101, "Lowbie", 12), char(r, 102, "Mainchar", 80), char(c, 101, "Classicguy", 60), char(e, 900, "Eraguy", 60)}, all))
	link, chars, err := store.get(ctx, 42)
	must(err)
	if link == nil || link.BattleTag != "Alice#1234" || len(chars) != 4 || chars[0].Name != "Mainchar" {
		t.Fatalf("after link: %+v %+v", link, chars)
	}

	// Mains: one per flavour, per guild; must be the right flavour's character.
	must(store.setPrimary(ctx, 1000, 42, r, "us", 102))
	must(store.setPrimary(ctx, 1000, 42, r, "us", 102)) // idempotent
	must(store.setPrimary(ctx, 1000, 42, c, "us", 101))
	must(store.setPrimary(ctx, 1000, 42, e, "us", 900))
	if err := store.setPrimary(ctx, 1000, 42, c, "us", 102); !errors.Is(err, errCharacterNotOwned) {
		t.Errorf("retail character as classic main = %v", err)
	}
	prim, err := store.getPrimaries(ctx, 1000, 42)
	must(err)
	if len(prim) != 3 || prim[r].Name != "Mainchar" || prim[c].Name != "Classicguy" || prim[e].Name != "Eraguy" {
		t.Fatalf("mains = %+v", prim)
	}

	// Refresh where Classic Era couldn't be read: Era characters are kept,
	// the others replaced (Mainchar deleted in game → its main goes away).
	must(store.save(ctx, 42, 555, "Alice#1234", []wowCharacter{char(r, 101, "Lowbie", 13), char(c, 101, "Classicguy", 61)}, []blizzard.Flavour{r, c}))
	_, chars, _ = store.get(ctx, 42)
	var names []string
	for _, ch := range chars {
		names = append(names, ch.Name)
	}
	if strings.Join(names, ",") != "Classicguy,Eraguy,Lowbie" {
		t.Errorf("after partial refresh: %v", names)
	}
	prim, _ = store.getPrimaries(ctx, 1000, 42)
	if _, ok := prim[r]; ok || prim[c].Name != "Classicguy" || prim[e].Name != "Eraguy" {
		t.Errorf("mains after refresh = %+v", prim)
	}

	// Another Discord user links the same Battle.net account: characters
	// move, and user 42's mains on them go away.
	must(store.save(ctx, 77, 555, "Alice#1234", []wowCharacter{char(c, 101, "Classicguy", 61)}, all))
	prim, _ = store.getPrimaries(ctx, 1000, 42)
	if _, ok := prim[c]; ok {
		t.Errorf("main on a character that moved to another user kept: %+v", prim)
	}

	// Switching user 42 to a different Battle.net account drops all their
	// old characters (including flavours not read this time), then unlink.
	must(store.save(ctx, 42, 666, "Alice#9999", nil, []blizzard.Flavour{r}))
	if _, chars, _ := store.get(ctx, 42); len(chars) != 0 {
		t.Errorf("old account's characters kept after switching accounts: %+v", chars)
	}
	must(store.setPrimary(ctx, 1000, 77, c, "us", 101))
	ok, err := store.unlink(ctx, 42)
	must(err)
	if !ok {
		t.Error("unlink reported nothing to unlink")
	}
	if link, _, _ := store.get(ctx, 42); link != nil {
		t.Errorf("still linked: %+v", link)
	}
	if prim, _ := store.getPrimaries(ctx, 1000, 77); prim[c].Name != "Classicguy" {
		t.Error("unlinking user 42 removed user 77's main")
	}

	rows, err := db.QueryContext(ctx, "SELECT CONCAT(battlenet_id, ':', action) FROM battlenet_link_history WHERE discord_user_id = 42 ORDER BY id")
	must(err)
	defer rows.Close()
	var hist []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		hist = append(hist, s)
	}
	if want := "555:linked,555:unlinked,666:linked,666:unlinked"; strings.Join(hist, ",") != want {
		t.Errorf("history = %v, want %s", hist, want)
	}
}
