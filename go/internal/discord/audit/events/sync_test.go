package events

import (
	"slices"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

func TestDiffMembers(t *testing.T) {
	syncStart := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	before := storedMember{updatedAt: syncStart.Add(-time.Hour)}
	during := storedMember{updatedAt: syncStart} // joined via gateway mid-sync

	current := map[snowflake.ID]currentMember{
		1: {username: "still-here"},
		2: {username: "joined-offline"},
	}
	stored := map[snowflake.ID]storedMember{
		1: before, // still here: no change
		3: before, // left while offline
		4: during, // joined after the member list was fetched: not a leave
	}

	joined, left := diffMembers(current, stored, syncStart)
	slices.Sort(joined)
	slices.Sort(left)

	if !slices.Equal(joined, []snowflake.ID{2}) {
		t.Errorf("joined = %v, want [2]", joined)
	}
	if !slices.Equal(left, []snowflake.ID{3}) {
		t.Errorf("left = %v, want [3]", left)
	}
}

func TestDiffMembersFirstSync(t *testing.T) {
	current := map[snowflake.ID]currentMember{1: {}, 2: {}}
	joined, left := diffMembers(current, map[snowflake.ID]storedMember{}, time.Now())
	if len(joined) != 2 || len(left) != 0 {
		t.Errorf("joined = %v, left = %v; want both members joined, none left", joined, left)
	}
}
