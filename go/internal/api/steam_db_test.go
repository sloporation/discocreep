package api

import (
	"context"
	"os"
	"strconv"
	"testing"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// TestDBSteamStore exercises steam_links / steam_link_history against a real
// MariaDB with migrations applied. Skipped unless BXT_TEST_DB_PORT is set, e.g.:
//
//	docker run --rm -d -p 13306:3306 -e MARIADB_ROOT_PASSWORD=t -e MARIADB_DATABASE=t mariadb:11
//	BXT_TEST_DB_PORT=13306 go test ./internal/api/ -run DBSteamStore
func TestDBSteamStore(t *testing.T) {
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
	store := &dbSteamStore{db: db}

	history := func() []string {
		rows, err := db.QueryContext(ctx, "SELECT CONCAT(steam_id, ':', action) FROM steam_link_history WHERE discord_user_id = 42 ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}

	if l, err := store.get(ctx, 42); err != nil || l != nil {
		t.Fatalf("before linking: %v %v", l, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.link(ctx, 42, 76561197960287930))
	must(store.link(ctx, 42, 76561197960287930)) // same account again: no new history
	must(store.link(ctx, 42, 76561197960287931)) // switch accounts

	l, err := store.get(ctx, 42)
	if err != nil || l == nil || l.SteamID != 76561197960287931 {
		t.Fatalf("after switching: %+v %v", l, err)
	}
	if ok, err := store.unlink(ctx, 42); err != nil || !ok {
		t.Fatalf("unlink: %v %v", ok, err)
	}
	if ok, _ := store.unlink(ctx, 42); ok {
		t.Error("second unlink reported success")
	}
	if l, _ := store.get(ctx, 42); l != nil {
		t.Errorf("still linked: %+v", l)
	}

	want := []string{
		"76561197960287930:linked",
		"76561197960287930:unlinked",
		"76561197960287931:linked",
		"76561197960287931:unlinked",
	}
	got := history()
	if len(got) != len(want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("history[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
