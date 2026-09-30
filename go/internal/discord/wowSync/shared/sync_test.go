package shared

import (
	"slices"
	"strings"
	"testing"

	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
)

func set(ids ...snowflake.ID) map[snowflake.ID]bool {
	m := map[snowflake.ID]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestPlan(t *testing.T) {
	const (
		officer, raider = snowflake.ID(101), snowflake.ID(102)
		unrelated       = snowflake.ID(900) // a role the sync must never touch
	)
	managed := set(officer, raider)
	nick := func(s string) *string { return &s }

	tests := []struct {
		name      string
		m         memberState
		desired   map[snowflake.ID]bool
		removable map[snowflake.ID]bool
		nick      *string
		wantRoles []snowflake.ID // nil = roles unchanged
		wantNick  *string        // nil = nickname unchanged
	}{
		{
			name:      "gains a desired role, keeps unrelated roles",
			m:         memberState{roles: []snowflake.ID{unrelated}},
			desired:   set(raider),
			removable: set(officer, raider),
			wantRoles: []snowflake.ID{unrelated, raider},
		},
		{
			name:      "promoted: old role removed, new added",
			m:         memberState{roles: []snowflake.ID{raider, unrelated}},
			desired:   set(officer),
			removable: set(officer, raider),
			wantRoles: []snowflake.ID{unrelated, officer},
		},
		{
			name:      "already has it: nothing to do",
			m:         memberState{roles: []snowflake.ID{officer}},
			desired:   set(officer),
			removable: set(officer, raider),
		},
		{
			name:      "no longer qualifies: loses managed roles only",
			m:         memberState{roles: []snowflake.ID{officer, unrelated}},
			desired:   set(),
			removable: set(officer, raider),
			wantRoles: []snowflake.ID{unrelated},
		},
		{
			name:      "auto-remove off (nothing removable): only adds",
			m:         memberState{roles: []snowflake.ID{officer}},
			desired:   set(raider),
			removable: set(),
			wantRoles: []snowflake.ID{officer, raider},
		},
		{
			name:      "role whose guild couldn't be read isn't removed",
			m:         memberState{roles: []snowflake.ID{officer, raider}},
			desired:   set(),
			removable: set(raider), // officer's guild failed this run
			wantRoles: []snowflake.ID{officer},
		},
		{
			name:     "nickname set",
			m:        memberState{nick: nick("old")},
			desired:  set(),
			nick:     nick("Thrall"),
			wantNick: nick("Thrall"),
		},
		{
			name:    "nickname already matches",
			m:       memberState{nick: nick("Thrall")},
			desired: set(),
			nick:    nick("Thrall"),
		},
		{
			name:    "server owner's nickname is never changed",
			m:       memberState{isOwner: true},
			desired: set(),
			nick:    nick("Thrall"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			update, changed := plan(tt.m, tt.desired, managed, tt.removable, tt.nick)

			if tt.wantRoles == nil {
				if update.Roles != nil {
					t.Errorf("roles changed to %v, want unchanged", *update.Roles)
				}
			} else if update.Roles == nil || !sameRoles(*update.Roles, tt.wantRoles) {
				t.Errorf("roles = %v, want %v", update.Roles, tt.wantRoles)
			}
			if update.Roles != nil && slices.Contains(tt.m.roles, unrelated) && !slices.Contains(*update.Roles, unrelated) {
				t.Error("an unmapped role was removed")
			}
			if tt.wantNick == nil {
				if update.Nick != nil {
					t.Errorf("nick changed to %q, want unchanged", *update.Nick)
				}
			} else if update.Nick == nil || *update.Nick != *tt.wantNick {
				t.Errorf("nick = %v, want %q", update.Nick, *tt.wantNick)
			}
			if want := tt.wantRoles != nil || tt.wantNick != nil; changed != want {
				t.Errorf("changed = %v, want %v", changed, want)
			}
		})
	}
}

func TestNickname(t *testing.T) {
	both := map[blizzard.Flavour]main{
		blizzard.Retail:  {characterName: "Thrall"},
		blizzard.Classic: {characterName: "Rexxar"},
	}
	retailOnly := map[blizzard.Flavour]main{blizzard.Retail: {characterName: "Thrall"}}
	classicOnly := map[blizzard.Flavour]main{blizzard.Classic: {characterName: "Rexxar"}}

	cases := []struct {
		name  string
		mode  NicknameMode
		mains map[blizzard.Flavour]main
		want  string // "" = leave alone
	}{
		{"off", NicknameOff, both, ""},
		{"retail", NicknameMode(blizzard.Retail), both, "Thrall"},
		{"classic", NicknameMode(blizzard.Classic), both, "Rexxar"},
		{"classic but no classic main", NicknameMode(blizzard.Classic), retailOnly, ""},
		{"combined, both", NicknameCombined, both, "Thrall / Rexxar"},
		{"combined, one", NicknameCombined, classicOnly, "Rexxar"},
		{"combined, none", NicknameCombined, map[blizzard.Flavour]main{}, ""},
	}
	for _, c := range cases {
		got := nickname(c.mode, c.mains)
		if (got == nil) != (c.want == "") || (got != nil && *got != c.want) {
			t.Errorf("%s: nickname = %v, want %q", c.name, got, c.want)
		}
	}

	long := map[blizzard.Flavour]main{
		blizzard.Retail:     {characterName: "Aaaaaaaaaaaa"},
		blizzard.Classic:    {characterName: "Bbbbbbbbbbbb"},
		blizzard.ClassicEra: {characterName: "Ççççççççççç"},
	}
	if got := nickname(NicknameCombined, long); got == nil || len([]rune(*got)) != maxNickLength || !strings.HasPrefix(*got, "Aaaaaaaaaaaa / Bbbbbbbbbbbb / ") {
		t.Errorf("long combined nickname = %v, want truncated to %d characters", got, maxNickLength)
	}
}

func TestRankKey(t *testing.T) {
	if got := (RankKey{LinkID: 12, Rank: 4}).String(); got != "12:4" {
		t.Errorf("RankKey.String = %q", got)
	}
}

func TestRoleChanges(t *testing.T) {
	const (
		officer, raider, alt, unrelated = snowflake.ID(101), snowflake.ID(102), snowflake.ID(103), snowflake.ID(900)
		user                            = snowflake.ID(7)
	)
	why := map[snowflake.ID]string{officer: "officer rank", raider: "raider rank"}
	reason := func(snowflake.ID) string { return "not granted" }
	got := roleChanges(user,
		[]snowflake.ID{officer, alt, unrelated},    // before
		[]snowflake.ID{officer, unrelated, raider}, // after: raider added, alt removed
		set(officer, raider),                       // desired
		set(raider),                                // held before this run... and a role taken off by hand:
		why, reason)
	want := []roleLogEntry{
		{user, officer, RoleAdopt, "officer rank"}, // had it, rank grants it, not yet held
		{user, raider, RoleAdd, "raider rank"},
		{user, alt, RoleRemove, "not granted"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("roleChanges = %+v, want %+v", got, want)
	}

	// A held role that's gone (taken off outside the bot) and isn't re-added is released.
	got = roleChanges(user, nil, nil, set(), set(alt), why, reason)
	if len(got) != 1 || got[0].action != RoleRelease || got[0].roleID != alt {
		t.Errorf("released = %+v", got)
	}
	// Held and still there: nothing to log.
	if got = roleChanges(user, []snowflake.ID{officer}, []snowflake.ID{officer}, set(officer), set(officer), why, reason); len(got) != 0 {
		t.Errorf("no change logged %+v", got)
	}
}
