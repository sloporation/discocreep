package events

import (
	"testing"

	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker/shared"
)

func TestDiffInvites(t *testing.T) {
	inv := func(uses int) shared.Invite { return shared.Invite{Uses: uses} }

	tests := []struct {
		name     string
		old, cur map[string]shared.Invite
		want     string // "" means nil
	}{
		{"no snapshot", nil, map[string]shared.Invite{"a": inv(1)}, ""},
		{"count went up", map[string]shared.Invite{"a": inv(1), "b": inv(4)}, map[string]shared.Invite{"a": inv(1), "b": inv(5)}, "b"},
		{"new invite with a use", map[string]shared.Invite{"a": inv(1)}, map[string]shared.Invite{"a": inv(1), "n": inv(1)}, "n"},
		{"new invite unused", map[string]shared.Invite{"a": inv(1)}, map[string]shared.Invite{"a": inv(1), "n": inv(0)}, ""},
		{"single-use invite consumed", map[string]shared.Invite{"a": inv(1), "s": inv(0)}, map[string]shared.Invite{"a": inv(1)}, "s"},
		{"two vanished is ambiguous", map[string]shared.Invite{"s": inv(0), "t": inv(0)}, map[string]shared.Invite{}, ""},
		{"nothing changed", map[string]shared.Invite{"a": inv(1)}, map[string]shared.Invite{"a": inv(1)}, ""},
		{"empty old snapshot is still a snapshot", map[string]shared.Invite{}, map[string]shared.Invite{"n": inv(1)}, "n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffInvites(tt.old, tt.cur)
			switch {
			case tt.want == "" && got != nil:
				t.Fatalf("got %q, want nil", got.Code)
			case tt.want != "" && (got == nil || got.Code != tt.want):
				t.Fatalf("got %v, want %q", got, tt.want)
			}
		})
	}
}
